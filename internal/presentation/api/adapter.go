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
	"fmt"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/observability"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Token           string
	ServiceAccounts []models.ServiceAccount
	Config          string
	Revision        string
	Digest          string
	mu              sync.RWMutex
	idempotency     map[string]Operation
	operations      map[string]Operation
	audit           []Audit
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
	TLSConfig    *tls.Config
}

// UpdateRuntimeRevision atomically updates the management snapshot metadata
// after an application-layer reload has prepared a new graph.
func (server *Server) UpdateRuntimeRevision(revision, digest string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.Revision = revision
	server.Digest = digest
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
type Audit struct {
	Timestamp time.Time `json:"timestamp"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Resource  string    `json:"resource"`
	Result    string    `json:"result"`
	RequestID string    `json:"requestId"`
}

func (server *Server) Handler() http.Handler {
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
	response.Header().Set("X-Request-ID", requestID)
	if request.URL.Path == "/healthz" {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "health endpoint accepts GET and HEAD", requestID)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "requestId": requestID})
		return
	}
	if request.Method != http.MethodGet && request.URL.Path == "/healthz" {
		writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", requestID)
		return
	}
	if !server.authorized(request.Header.Get("Authorization")) {
		writeProblem(response, 401, "unauthorized", "management authentication required", requestID)
		return
	}
	path := strings.TrimSuffix(request.URL.Path, "/")
	switch {
	case path == "/api/status" && request.Method == http.MethodGet:
		server.mu.RLock()
		defer server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"revision": server.Revision, "digest": server.Digest, "listeners": server.Listeners, "upstreams": server.Upstreams, "plugins": server.Plugins, "requestId": requestID})
	case path == "/api/listeners" && request.Method == http.MethodGet:
		server.writePage(response, server.Listeners, request, requestID)
	case path == "/api/upstreams" && request.Method == http.MethodGet:
		server.writePage(response, server.Upstreams, request, requestID)
	case path == "/api/sites" && request.Method == http.MethodGet:
		server.writePage(response, server.Sites, request, requestID)
	case path == "/api/plugins" && request.Method == http.MethodGet:
		server.writePage(response, server.Plugins, request, requestID)
	case path == "/api/operations" && request.Method == http.MethodGet:
		server.mu.RLock()
		items := make([]Operation, 0, len(server.operations))
		for _, operation := range server.operations {
			items = append(items, operation)
		}
		server.mu.RUnlock()
		server.writePage(response, items, request, requestID)
	case path == "/api/config" && request.Method == http.MethodGet:
		server.mu.RLock()
		defer server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"revision": server.Revision, "digest": server.Digest, "yaml": redact(server.Config), "requestId": requestID})
	case path == "/api/config" && request.Method == http.MethodPut:
		var input struct {
			YAML string `json:"yaml"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			writeProblem(response, 400, "invalid_input", "request body must be JSON", requestID)
			return
		}
		if expected := request.Header.Get("If-Match"); expected != "" && expected != server.Revision {
			writeProblem(response, 409, "conflict", "configuration revision does not match If-Match", requestID)
			return
		}
		if server.ValidateConfig != nil {
			if err := server.ValidateConfig(input.YAML); err != nil {
				writeProblem(response, 422, "config_invalid", err.Error(), requestID)
				return
			}
		}
		server.mu.Lock()
		server.Config = input.YAML
		server.Revision = randomID()
		digest := sha256.Sum256([]byte(input.YAML))
		server.Digest = hex.EncodeToString(digest[:])
		server.mu.Unlock()
		writeJSON(response, 202, map[string]any{"revision": server.Revision, "digest": server.Digest, "requestId": requestID})
	case strings.HasPrefix(path, "/api/sites/") && strings.HasSuffix(path, "/publish") && request.Method == http.MethodPost:
		server.handleSitePublish(response, request, path, requestID)
	case strings.HasPrefix(path, "/api/sites/") && strings.HasSuffix(path, "/rollback") && request.Method == http.MethodPost:
		server.handleSiteRollback(response, request, path, requestID)
	case path == "/api/config/validate" && request.Method == http.MethodPost:
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
	case (path == "/api/config/reload" || path == "/api/reload") && request.Method == http.MethodPost:
		if expected := request.Header.Get("If-Match"); expected != "" && expected != server.Revision {
			writeProblem(response, 409, "conflict", "configuration revision does not match If-Match", requestID)
			return
		}
		if server.ReloadConfig == nil {
			writeProblem(response, 501, "not_implemented", "configuration reload is unavailable", requestID)
			return
		}
		op, err := server.ReloadConfig(request.Context(), server.Revision)
		if err != nil {
			writeProblem(response, 422, "config_invalid", err.Error(), requestID)
			return
		}
		server.mu.Lock()
		if server.operations == nil {
			server.operations = make(map[string]Operation)
		}
		server.operations[op.ID] = op
		server.mu.Unlock()
		writeJSON(response, 202, map[string]any{"operationId": op.ID, "state": op.State, "requestId": requestID})
	case strings.HasPrefix(path, "/api/tls/") && (strings.HasSuffix(path, "/renew") || strings.HasSuffix(path, "/revoke")) && request.Method == http.MethodPost:
		server.handleTLSOperation(response, request, path, requestID)
	case path == "/api/audit" && request.Method == http.MethodGet:
		server.mu.RLock()
		items := append([]Audit(nil), server.audit...)
		server.mu.RUnlock()
		server.writePage(response, items, request, requestID)
	case path == "/metrics" && request.Method == http.MethodGet:
		if server.Metrics != nil {
			server.Metrics.Handler().ServeHTTP(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintln(response, "# HELP liapoldus_management_requests_total Management API requests")
		_, _ = fmt.Fprintln(response, "# TYPE liapoldus_management_requests_total counter")
	case path == "/api/plugins/admin-surfaces" && request.Method == http.MethodGet:
		server.mu.RLock()
		surfaces := append([]AdminSurface(nil), server.AdminSurfaces...)
		server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"items": surfaces, "requestId": requestID})
	case strings.HasPrefix(path, "/api/plugins/") && strings.Contains(path, "/admin/pages/") && (request.Method == http.MethodGet || request.Method == http.MethodPost):
		server.handlePluginAdmin(response, request, path, requestID)
	case strings.HasPrefix(path, "/api/plugins/") && strings.HasSuffix(path, "/restart") && request.Method == http.MethodPost:
		if server.RestartPlugin == nil {
			writeProblem(response, 501, "not_implemented", "plugin restart is unavailable", requestID)
			return
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		instance := parts[2]
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
	case strings.HasPrefix(path, "/api/operations/") && request.Method == http.MethodGet:
		id := strings.TrimPrefix(path, "/api/operations/")
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
	case []Audit:
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
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "tls" || parts[2] == "" {
		writeProblem(response, http.StatusNotFound, "not_found", "TLS issuer resource not found", requestID)
		return
	}
	idempotencyKey := request.Header.Get("Idempotency-Key")
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 128 || !ascii(idempotencyKey) {
		writeProblem(response, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must contain 16-128 ASCII characters", requestID)
		return
	}
	var operation func(context.Context, string, string) (Operation, error)
	if parts[3] == "renew" {
		operation = server.RenewTLS
	} else {
		operation = server.RevokeTLS
	}
	if operation == nil {
		writeProblem(response, http.StatusNotImplemented, "not_implemented", "TLS issuer operation is unavailable", requestID)
		return
	}
	op, err := operation(request.Context(), parts[2], idempotencyKey)
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

func (server *Server) handleSitePublish(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.PublishSite == nil {
		writeProblem(response, http.StatusServiceUnavailable, "plugin_unavailable", "registry publisher is unavailable", requestID)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "sites" || parts[2] == "" {
		writeProblem(response, http.StatusNotFound, "not_found", "site resource not found", requestID)
		return
	}
	var input struct {
		Source         string `json:"source"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Source == "" || len(input.IdempotencyKey) < 16 {
		writeProblem(response, http.StatusBadRequest, "invalid_input", "source and idempotencyKey are required", requestID)
		return
	}
	cacheKey := parts[2] + "|" + input.IdempotencyKey
	server.mu.RLock()
	previous, cached := server.idempotency[cacheKey]
	server.mu.RUnlock()
	if cached {
		writeJSON(response, http.StatusCreated, map[string]any{"operationId": previous.ID, "state": previous.State, "requestId": requestID})
		return
	}
	operation, err := server.PublishSite(request.Context(), parts[2], input.Source, input.IdempotencyKey)
	if err != nil {
		writeProblem(response, http.StatusUnprocessableEntity, "publish_failed", err.Error(), requestID)
		return
	}
	server.mu.Lock()
	if server.idempotency == nil {
		server.idempotency = make(map[string]Operation)
	}
	if previous, exists := server.idempotency[cacheKey]; exists {
		operation = previous
	} else {
		server.idempotency[cacheKey] = operation
	}
	if server.operations == nil {
		server.operations = make(map[string]Operation)
	}
	server.operations[operation.ID] = operation
	server.mu.Unlock()
	writeJSON(response, http.StatusCreated, map[string]any{"operationId": operation.ID, "state": operation.State, "requestId": requestID})
}

func (server *Server) handleSiteRollback(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.RollbackSite == nil {
		writeProblem(response, http.StatusServiceUnavailable, "registry_unavailable", "registry rollback is unavailable", requestID)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "sites" || parts[2] == "" {
		writeProblem(response, http.StatusNotFound, "not_found", "site resource not found", requestID)
		return
	}
	operation, err := server.RollbackSite(request.Context(), parts[2], request.Header.Get("Idempotency-Key"))
	if err != nil {
		writeProblem(response, http.StatusUnprocessableEntity, "rollback_failed", err.Error(), requestID)
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
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 6 {
		writeProblem(response, 404, "not_found", "plugin admin resource not found", requestID)
		return
	}
	instance, page := parts[2], parts[5]
	action := ""
	if len(parts) == 7 {
		action = parts[6]
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
func (server *Server) authorized(value string) bool {
	if server.Token == "" && len(server.ServiceAccounts) == 0 {
		return true
	}
	if !strings.HasPrefix(value, "Bearer ") {
		return false
	}
	key := strings.TrimPrefix(value, "Bearer ")
	if server.Token != "" && key == server.Token {
		return true
	}
	for _, account := range server.ServiceAccounts {
		if bcrypt.CompareHashAndPassword([]byte(account.KeyHash), []byte(key)) == nil {
			return true
		}
	}
	return false
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
