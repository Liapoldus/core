// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/observability"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	Token           string
	ServiceAccounts []models.ServiceAccount
	Config          string
	Revision        string
	Digest          string
	mu              sync.RWMutex
	idempotency     map[string]idempotencyRecord
	publishMu       sync.Mutex
	operations      map[string]Operation
	AdminSurfaces   []AdminSurface
	AdminDispatcher *plugins.Dispatcher
	Listeners       []any
	Upstreams       []any
	Plugins         []any
	Sites           []any
	RestartPlugin   func(context.Context, string) (Operation, error)
	ValidateConfig  func(string) error
	ReloadConfig    func(context.Context, string) (Operation, error)
	// RenewTLS and RevokeTLS are the typed boundary to the configured TLS issuer.
	// The API adapter never receives certificate material or storage paths.
	RenewTLS     func(context.Context, string, string) (Operation, error)
	RevokeTLS    func(context.Context, string, string) (Operation, error)
	PublishSite  func(context.Context, string, string, string) (Operation, error)
	RollbackSite func(context.Context, string, string) (Operation, error)
	Metrics      *observability.Registry
	Audit        *application.AuditService
	AuditWords   config.ObservabilityWords
	Management   config.ManagementWords
	Errors       config.ErrorCatalog
	SiteSources  map[string]models.Site
	TLSConfig    *tls.Config
	contractOnce sync.Once
}

// UpdateRuntimeRevision atomically updates the management snapshot metadata
// after an application-layer reload has prepared a new graph.
func (server *Server) UpdateRuntimeRevision(revision, digest string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.Revision = revision
	server.Digest = digest
}

func (server *Server) UpdateRuntimeConfig(revision, digest, configuration string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.Revision = revision
	server.Digest = digest
	server.Config = configuration
}

type AdminSurface struct {
	Plugin       string   `json:"plugin"`
	Namespace    string   `json:"namespace"`
	Version      string   `json:"version"`
	Title        string   `json:"title"`
	Capabilities []string `json:"capabilities"`
}
type Operation struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
	Result    any       `json:"result,omitempty"`
}

type idempotencyRecord struct {
	Fingerprint string
	Operation   Operation
	RequestID   string
	ExpiresAt   time.Time
}

func (server *Server) Handler() http.Handler {
	server.contractOnce.Do(func() {
		if server.Management.Paths.Sites != "" {
			if _, exists := server.Errors.Lookup(server.Management.Codes.NoPreviousRelease); exists {
				return
			}
		} else {
			server.Management, _ = config.LoadManagement()
		}
		server.Errors, _ = config.LoadErrorCatalog()
	})
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		wrapped := &metricResponseWriter{ResponseWriter: response}
		server.handle(wrapped, request)
		if server.Metrics != nil {
			status := wrapped.status
			if status == 0 {
				status = http.StatusOK
			}
			server.Metrics.ObserveManagement(request.Method, strconv.Itoa(status))
		}
	})
}

type metricResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *metricResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (server *Server) Listen(ctx context.Context, address string) error {
	httpServer := &http.Server{Addr: address, Handler: server.Handler()}
	result := make(chan error, 1)
	go func() {
		if server.TLSConfig != nil {
			httpServer.TLSConfig = server.TLSConfig
			result <- httpServer.ListenAndServeTLS("", "")
			return
		}
		result <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownContext)
	}
}
func (server *Server) handle(response http.ResponseWriter, request *http.Request) {
	requestID := "req_" + randomID()
	response.Header().Set(server.Management.Headers.RequestID, requestID)
	if request.URL.Path == server.Management.Paths.Healthz {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "health endpoint accepts GET and HEAD", requestID)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "requestId": requestID})
		return
	}
	actor, authorized := server.authenticate(request.Header.Get("Authorization"))
	if !authorized {
		writeProblem(response, 401, "unauthorized", "management authentication required", requestID)
		return
	}
	path := strings.TrimSuffix(request.URL.Path, "/")
	switch {
	case path == server.Management.Paths.Status && request.Method == http.MethodGet:
		server.mu.RLock()
		defer server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"revision": server.Revision, "digest": server.Digest, "listeners": server.Listeners, "upstreams": server.Upstreams, "plugins": server.Plugins, "requestId": requestID})
	case path == server.Management.Paths.Listeners && request.Method == http.MethodGet:
		server.writePage(response, server.Listeners, request, requestID)
	case path == server.Management.Paths.Upstreams && request.Method == http.MethodGet:
		server.writePage(response, server.Upstreams, request, requestID)
	case path == server.Management.Paths.Sites && request.Method == http.MethodGet:
		server.writePage(response, server.Sites, request, requestID)
	case path == server.Management.Paths.Plugins && request.Method == http.MethodGet:
		server.writePage(response, server.Plugins, request, requestID)
	case path == server.Management.Paths.Operations && request.Method == http.MethodGet:
		server.mu.RLock()
		items := make([]Operation, 0, len(server.operations))
		for _, operation := range server.operations {
			items = append(items, operation)
		}
		server.mu.RUnlock()
		server.writePage(response, items, request, requestID)
	case path == server.Management.Paths.Config && request.Method == http.MethodGet:
		server.mu.RLock()
		defer server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"revision": server.Revision, "digest": server.Digest, "yaml": redact(server.Config), "requestId": requestID})
	case path == server.Management.Paths.Config && request.Method == http.MethodPut:
		server.mu.RLock()
		digestBefore := server.Digest
		currentRevision := server.Revision
		server.mu.RUnlock()
		var input struct {
			YAML string `json:"yaml"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.ConfigUpdate, server.AuditWords.Audit.Resources.Gateway, server.AuditWords.Audit.Results.Failed, requestID, digestBefore, digestBefore)
			writeProblem(response, 400, "invalid_input", "request body must be JSON", requestID)
			return
		}
		if expected := request.Header.Get("If-Match"); expected != "" && expected != currentRevision {
			server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.ConfigUpdate, server.AuditWords.Audit.Resources.Gateway, server.AuditWords.Audit.Results.Failed, requestID, digestBefore, digestBefore)
			writeProblem(response, 409, "conflict", "configuration revision does not match If-Match", requestID)
			return
		}
		if server.ValidateConfig != nil {
			if err := server.ValidateConfig(input.YAML); err != nil {
				server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.ConfigUpdate, server.AuditWords.Audit.Resources.Gateway, server.AuditWords.Audit.Results.Failed, requestID, digestBefore, digestBefore)
				writeProblem(response, 422, "config_invalid", err.Error(), requestID)
				return
			}
		}
		server.mu.Lock()
		server.Config = input.YAML
		server.Revision = randomID()
		digest := sha256.Sum256([]byte(input.YAML))
		server.Digest = hex.EncodeToString(digest[:])
		digestAfter := server.Digest
		server.mu.Unlock()
		server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.ConfigUpdate, server.AuditWords.Audit.Resources.Gateway, server.AuditWords.Audit.Results.Succeeded, requestID, digestBefore, digestAfter)
		writeJSON(response, 202, map[string]any{"revision": server.Revision, "digest": server.Digest, "requestId": requestID})
	case strings.HasPrefix(path, server.Management.Paths.Sites+"/") && strings.HasSuffix(path, "/"+server.Management.Paths.Publish) && request.Method == http.MethodPost:
		server.handleSitePublish(response, request, path, actor, requestID)
	case strings.HasPrefix(path, server.Management.Paths.Sites+"/") && strings.HasSuffix(path, "/"+server.Management.Paths.Rollback) && request.Method == http.MethodPost:
		server.handleSiteRollback(response, request, path, requestID)
	case path == server.Management.Paths.ConfigValidate && request.Method == http.MethodPost:
		var input struct {
			YAML string `json:"yaml"`
		}
		_ = json.NewDecoder(request.Body).Decode(&input)
		if server.ValidateConfig != nil {
			if err := server.ValidateConfig(input.YAML); err != nil {
				writeProblem(response, 422, "config_invalid", err.Error(), requestID)
				return
			}
		}
		writeJSON(response, 200, map[string]any{"revision": server.Revision, "digest": server.Digest, "valid": true, "requestId": requestID})
	case (path == server.Management.Paths.ConfigReload || path == server.Management.Paths.Reload) && request.Method == http.MethodPost:
		if expected := request.Header.Get("If-Match"); expected != "" && expected != server.Revision {
			writeProblem(response, 409, "conflict", "configuration revision does not match If-Match", requestID)
			return
		}
		if server.ReloadConfig == nil {
			writeProblem(response, 501, "not_implemented", "configuration reload is unavailable", requestID)
			return
		}
		server.mu.RLock()
		digestBefore := server.Digest
		revision := server.Revision
		server.mu.RUnlock()
		op, err := server.ReloadConfig(request.Context(), revision)
		if err != nil {
			server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.ConfigReload, server.AuditWords.Audit.Resources.Gateway, server.AuditWords.Audit.Results.Failed, requestID, digestBefore, digestBefore)
			writeProblem(response, 422, "config_invalid", err.Error(), requestID)
			return
		}
		server.mu.Lock()
		if server.operations == nil {
			server.operations = make(map[string]Operation)
		}
		server.operations[op.ID] = op
		digestAfter := server.Digest
		server.mu.Unlock()
		server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.ConfigReload, server.AuditWords.Audit.Resources.Gateway, server.AuditWords.Audit.Results.Succeeded, requestID, digestBefore, digestAfter)
		writeJSON(response, 202, map[string]any{"operationId": op.ID, "state": op.State, "requestId": requestID})
	case strings.HasPrefix(path, server.Management.Paths.TLS+"/") && (strings.HasSuffix(path, "/"+server.Management.Paths.Renew) || strings.HasSuffix(path, "/"+server.Management.Paths.Revoke)) && request.Method == http.MethodPost:
		server.handleTLSOperation(response, request, path, requestID)
	case path == server.Management.Paths.Audit && request.Method == http.MethodGet:
		if server.Audit == nil {
			server.writePage(response, []models.AuditRecord{}, request, requestID)
			return
		}
		items, err := server.Audit.Records(request.Context())
		if err != nil {
			writeProblem(response, http.StatusServiceUnavailable, server.AuditWords.Audit.StorageUnavailable.Code, server.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		server.writePage(response, items, request, requestID)
	case path == server.Management.Paths.Metrics && request.Method == http.MethodGet:
		if server.Metrics != nil {
			server.Metrics.Handler().ServeHTTP(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintln(response, "# HELP liapoldus_management_requests_total Management API requests")
		_, _ = fmt.Fprintln(response, "# TYPE liapoldus_management_requests_total counter")
	case path == server.Management.Paths.AdminSurfaces && request.Method == http.MethodGet:
		server.mu.RLock()
		surfaces := append([]AdminSurface(nil), server.AdminSurfaces...)
		server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"items": surfaces, "requestId": requestID})
	case strings.HasPrefix(path, server.Management.Paths.Plugins+"/") && strings.Contains(path, "/"+server.Management.Paths.AdminPages+"/") && (request.Method == http.MethodGet || request.Method == http.MethodPost):
		server.handlePluginAdmin(response, request, path, requestID)
	case strings.HasPrefix(path, server.Management.Paths.Plugins+"/") && strings.HasSuffix(path, "/"+server.Management.Paths.Restart) && request.Method == http.MethodPost:
		if server.RestartPlugin == nil {
			writeProblem(response, 501, "not_implemented", "plugin restart is unavailable", requestID)
			return
		}
		instance := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.Plugins+"/"), "/"+server.Management.Paths.Restart)
		if len(instance) == 0 || strings.Contains(instance, "/") {
			writeProblem(response, http.StatusNotFound, "not_found", "plugin resource not found", requestID)
			return
		}
		op, err := server.RestartPlugin(request.Context(), instance)
		if err != nil {
			writeProblem(response, 422, "operation_failed", err.Error(), requestID)
			return
		}
		server.mu.Lock()
		if server.operations == nil {
			server.operations = map[string]Operation{}
		}
		server.operations[op.ID] = op
		server.mu.Unlock()
		writeJSON(response, 202, map[string]any{"operationId": op.ID, "requestId": requestID})
	case strings.HasPrefix(path, server.Management.Paths.Operations+"/") && request.Method == http.MethodGet:
		id := strings.TrimPrefix(path, server.Management.Paths.Operations+"/")
		server.mu.RLock()
		operation, ok := server.operations[id]
		server.mu.RUnlock()
		if !ok {
			writeProblem(response, 404, "not_found", "operation not found", requestID)
			return
		}
		writeJSON(response, 200, map[string]any{"id": operation.ID, "state": operation.State, "createdAt": operation.CreatedAt, "requestId": requestID})
	default:
		writeProblem(response, 404, "not_found", "resource not found", requestID)
	}
}

func (server *Server) writePage(response http.ResponseWriter, values any, request *http.Request, requestID string) {
	// Lists are kept as typed slices internally; pagination is an opaque offset cursor.
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if limit < 1 || limit > 100 {
		writeProblem(response, 400, "invalid_pagination", "limit must be between 1 and 100", requestID)
		return
	}
	offset := 0
	if cursor := request.URL.Query().Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		parsed, parseErr := strconv.Atoi(string(decoded))
		if err != nil || parseErr != nil || parsed < 0 {
			writeProblem(response, 400, "invalid_cursor", "cursor is invalid", requestID)
			return
		} else {
			offset = parsed
		}
	}
	items := sliceValues(values)
	if offset > len(items) {
		writeProblem(response, 400, "invalid_cursor", "cursor is invalid", requestID)
		return
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	var next any
	if end < len(items) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	writeJSON(response, 200, map[string]any{"items": items[offset:end], "nextCursor": next, "requestId": requestID})
}

func sliceValues(values any) []any {
	switch typed := values.(type) {
	case []any:
		return typed
	case []Operation:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = typed[i]
		}
		return result
	case []models.AuditRecord:
		result := make([]any, len(typed))
		for i := range typed {
			result[i] = typed[i]
		}
		return result
	default:
		return nil
	}
}

func (server *Server) handleTLSOperation(response http.ResponseWriter, request *http.Request, path, requestID string) {
	resource := strings.TrimPrefix(path, server.Management.Paths.TLS+"/")
	parts := strings.Split(resource, "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != server.Management.Paths.Renew && parts[1] != server.Management.Paths.Revoke) {
		writeProblem(response, http.StatusNotFound, "not_found", "TLS issuer resource not found", requestID)
		return
	}
	idempotencyKey := request.Header.Get("Idempotency-Key")
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 128 || !ascii(idempotencyKey) {
		writeProblem(response, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must contain 16-128 ASCII characters", requestID)
		return
	}
	var operation func(context.Context, string, string) (Operation, error)
	if parts[1] == server.Management.Paths.Renew {
		operation = server.RenewTLS
	} else {
		operation = server.RevokeTLS
	}
	if operation == nil {
		writeProblem(response, http.StatusNotImplemented, "not_implemented", "TLS issuer operation is unavailable", requestID)
		return
	}
	op, err := operation(request.Context(), parts[0], idempotencyKey)
	if err != nil {
		writeProblem(response, http.StatusUnprocessableEntity, "tls_operation_failed", err.Error(), requestID)
		return
	}
	server.mu.Lock()
	if server.operations == nil {
		server.operations = make(map[string]Operation)
	}
	server.operations[op.ID] = op
	server.mu.Unlock()
	writeJSON(response, http.StatusAccepted, map[string]any{"operationId": op.ID, "state": op.State, "requestId": requestID})
}

func (server *Server) handleSitePublish(response http.ResponseWriter, request *http.Request, path, actor, requestID string) {
	sitePrefix := server.Management.Paths.Sites + "/"
	siteSuffix := "/" + server.Management.Paths.Publish
	if !strings.HasPrefix(path, sitePrefix) || !strings.HasSuffix(path, siteSuffix) {
		writeProblem(response, http.StatusNotFound, "not_found", "site resource not found", requestID)
		return
	}
	site := strings.TrimSuffix(strings.TrimPrefix(path, sitePrefix), siteSuffix)
	if site == "" || strings.Contains(site, "/") {
		writeProblem(response, http.StatusNotFound, "not_found", "site resource not found", requestID)
		return
	}
	if server.SiteSources != nil {
		definition, exists := server.SiteSources[site]
		if !exists {
			writeProblem(response, http.StatusNotFound, "not_found", "site resource not found", requestID)
			return
		}
		if definition.Source != models.SourceRelease {
			server.writeCatalogProblem(response, server.Management.Codes.SiteSourceImmutable, requestID)
			return
		}
	}
	if server.PublishSite == nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	var input struct {
		Source         string
		IdempotencyKey string
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(request.Body)
	decodeErr := decoder.Decode(&fields)
	if decodeErr == nil {
		for key := range fields {
			if key != server.Management.JSON.Source && key != server.Management.JSON.IdempotencyKey {
				decodeErr = fmt.Errorf("unexpected publish request property")
				break
			}
		}
	}
	if decodeErr == nil {
		var trailing json.RawMessage
		if trailingErr := decoder.Decode(&trailing); trailingErr != io.EOF {
			decodeErr = fmt.Errorf("unexpected trailing publish request data")
		}
	}
	if decodeErr == nil {
		decodeErr = json.Unmarshal(fields[server.Management.JSON.Source], &input.Source)
	}
	if decodeErr == nil {
		decodeErr = json.Unmarshal(fields[server.Management.JSON.IdempotencyKey], &input.IdempotencyKey)
	}
	if decodeErr != nil || input.Source == "" || len(input.IdempotencyKey) < server.Management.Idempotency.KeyMin || len(input.IdempotencyKey) > server.Management.Idempotency.KeyChars || !ascii(input.IdempotencyKey) {
		writeProblem(response, http.StatusBadRequest, "invalid_input", "source and idempotencyKey are required", requestID)
		return
	}
	window, windowErr := time.ParseDuration(server.Management.Idempotency.Window)
	if windowErr != nil || window <= 0 {
		writeProblem(response, http.StatusInternalServerError, "config_invalid", "management idempotency contract is invalid", requestID)
		return
	}
	cacheKey := idempotencyKey(site, actor, input.IdempotencyKey)
	fingerprint := publishFingerprint(input.Source)
	server.publishMu.Lock()
	defer server.publishMu.Unlock()
	now := time.Now().UTC()
	server.mu.Lock()
	for key, record := range server.idempotency {
		if !now.Before(record.ExpiresAt) {
			delete(server.idempotency, key)
		}
	}
	previous, cached := server.idempotency[cacheKey]
	server.mu.Unlock()
	if cached {
		if previous.Fingerprint != fingerprint {
			server.writeCatalogProblem(response, server.Management.Codes.IdempotencyConflict, requestID)
			return
		} else {
			response.Header().Set(server.Management.Headers.RequestID, previous.RequestID)
			writeJSON(response, http.StatusCreated, map[string]any{
				server.Management.JSON.OperationID: previous.Operation.ID,
				server.Management.JSON.State:       previous.Operation.State,
				server.Management.JSON.RequestID:   previous.RequestID,
			})
			return
		}
	}
	digestBefore := server.runtimeDigest()
	operation, err := server.PublishSite(request.Context(), site, input.Source, input.IdempotencyKey)
	if err != nil {
		server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.SitePublished, site, server.AuditWords.Audit.Results.Failed, requestID, digestBefore, digestBefore)
		server.writeCatalogProblem(response, server.Management.Codes.ReleaseInvalid, requestID)
		return
	}
	operation.CreatedAt = now
	server.mu.Lock()
	if server.idempotency == nil {
		server.idempotency = make(map[string]idempotencyRecord)
	}
	server.idempotency[cacheKey] = idempotencyRecord{Fingerprint: fingerprint, Operation: operation, RequestID: requestID, ExpiresAt: operation.CreatedAt.Add(window)}
	if server.operations == nil {
		server.operations = make(map[string]Operation)
	}
	server.operations[operation.ID] = operation
	server.mu.Unlock()
	server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.SitePublished, site, server.AuditWords.Audit.Results.Succeeded, requestID, digestBefore, server.runtimeDigest())
	writeJSON(response, http.StatusCreated, map[string]any{
		server.Management.JSON.OperationID: operation.ID,
		server.Management.JSON.State:       operation.State,
		server.Management.JSON.RequestID:   requestID,
	})
}

func idempotencyKey(site, actor, key string) string {
	value, _ := json.Marshal(struct {
		Site  string
		Actor string
		Key   string
	}{Site: site, Actor: actor, Key: key})
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func publishFingerprint(source string) string {
	value, _ := json.Marshal(struct {
		Source string
	}{Source: source})
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func (server *Server) runtimeDigest() string {
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.Digest
}

func (server *Server) writeCatalogProblem(response http.ResponseWriter, code, requestID string) {
	problem, exists := server.Errors.Lookup(code)
	if !exists {
		writeProblem(response, http.StatusInternalServerError, "config_invalid", "management error contract is invalid", requestID)
		return
	}
	writeProblem(response, problem.Status, problem.Code, problem.Detail, requestID)
}

func (server *Server) handleSiteRollback(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.RollbackSite == nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	sitePrefix := server.Management.Paths.Sites + "/"
	siteSuffix := "/" + server.Management.Paths.Rollback
	if !strings.HasPrefix(path, sitePrefix) || !strings.HasSuffix(path, siteSuffix) {
		writeProblem(response, http.StatusNotFound, "not_found", "site resource not found", requestID)
		return
	}
	site := strings.TrimSuffix(strings.TrimPrefix(path, sitePrefix), siteSuffix)
	if site == "" || strings.Contains(site, "/") {
		writeProblem(response, http.StatusNotFound, "not_found", "site resource not found", requestID)
		return
	}
	if server.SiteSources != nil {
		definition, exists := server.SiteSources[site]
		if !exists {
			writeProblem(response, http.StatusNotFound, "not_found", "site resource not found", requestID)
			return
		}
		if definition.Source != models.SourceRelease {
			server.writeCatalogProblem(response, server.Management.Codes.SiteSourceImmutable, requestID)
			return
		}
	}
	operation, err := server.RollbackSite(request.Context(), site, request.Header.Get("Idempotency-Key"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			server.writeCatalogProblem(response, server.Management.Codes.NoPreviousRelease, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	server.mu.Lock()
	if server.operations == nil {
		server.operations = make(map[string]Operation)
	}
	server.operations[operation.ID] = operation
	server.mu.Unlock()
	writeJSON(response, http.StatusAccepted, map[string]any{"operationId": operation.ID, "state": operation.State, "requestId": requestID})
}

func ascii(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func (server *Server) handlePluginAdmin(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.AdminDispatcher == nil {
		writeProblem(response, 503, "plugin_unavailable", "plugin admin surface is unavailable", requestID)
		return
	}
	instanceAndRoute := strings.TrimPrefix(path, server.Management.Paths.Plugins+"/")
	instance, route, found := strings.Cut(instanceAndRoute, "/")
	pagePrefix := server.Management.Paths.AdminPages + "/"
	if !found || instance == "" || !strings.HasPrefix(route, pagePrefix) {
		writeProblem(response, 404, "not_found", "plugin admin resource not found", requestID)
		return
	}
	pageAndAction := strings.TrimPrefix(route, pagePrefix)
	parts := strings.Split(pageAndAction, "/")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		writeProblem(response, 404, "not_found", "plugin admin resource not found", requestID)
		return
	}
	page := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	var input json.RawMessage
	if request.Method == http.MethodPost {
		defer request.Body.Close()
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil && err.Error() != "EOF" {
			writeProblem(response, 400, "invalid_input", "request body must be JSON", requestID)
			return
		}
	}
	result, err := server.AdminDispatcher.Dispatch(request.Context(), instance, plugins.RequestContext{Instance: instance, Page: page, Action: action, Method: request.Method, RequestID: requestID, Actor: "management", Input: input})
	if err != nil {
		writeProblem(response, 502, "plugin_error", err.Error(), requestID)
		return
	}
	if result.Status == 0 {
		result.Status = 200
	}
	if result.ContentType == "" {
		result.ContentType = "application/json"
	}
	response.Header().Set("Content-Type", result.ContentType)
	response.WriteHeader(result.Status)
	_, _ = response.Write(result.Body)
}
func randomID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(bytes)
}
func (server *Server) authenticate(value string) (string, bool) {
	if server.Token == "" && len(server.ServiceAccounts) == 0 {
		return server.AuditWords.Audit.Actors.Anonymous, true
	}
	if !strings.HasPrefix(value, "Bearer ") {
		return "", false
	}
	key := strings.TrimPrefix(value, "Bearer ")
	if server.Token != "" && key == server.Token {
		return server.AuditWords.Audit.Actors.StaticToken, true
	}
	for _, account := range server.ServiceAccounts {
		if bcrypt.CompareHashAndPassword([]byte(account.KeyHash), []byte(key)) == nil {
			return account.ID, true
		}
	}
	return "", false
}

func (server *Server) recordAudit(ctx context.Context, actor, action, resource, result, requestID, digestBefore, digestAfter string) {
	if server.Audit == nil || action == "" || actor == "" {
		return
	}
	record := models.AuditRecord{
		Timestamp:    time.Now().UTC(),
		Actor:        actor,
		Action:       action,
		Resource:     resource,
		Result:       result,
		RequestID:    requestID,
		DigestBefore: digestBefore,
		DigestAfter:  digestAfter,
	}
	if server.Audit.Record(ctx, record) == nil && server.Metrics != nil {
		server.Metrics.ObserveAudit(action, result)
	}
}
func redact(value string) string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		for _, key := range []string{"statictoken:", "keyhash:", "clientsecret:", "secret:"} {
			if strings.HasPrefix(lower, key) {
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				lines[index] = indent + key + " ***"
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}
func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
func writeProblem(response http.ResponseWriter, status int, code, detail, requestID string) {
	response.Header().Set("Content-Type", "application/problem+json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]any{"type": "about:blank", "title": code, "status": status, "code": code, "detail": detail, "instance": "", "requestId": requestID})
}
