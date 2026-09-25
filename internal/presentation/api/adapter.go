// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	Token                    string
	ServiceAccounts          []models.ServiceAccount
	mu                       sync.RWMutex
	operations               map[string]Operation
	AdminSurfaces            []AdminSurface
	AdminDispatcher          *plugins.Dispatcher
	Plugins                  []any
	RestartPlugin            func(context.Context, string) (Operation, error)
	Audit                    *application.AuditService
	GroupService             application.GroupService
	AccessService            *application.AccessService
	CaddyVariant             string
	CaddyBuildID             string
	CaddyModules             []string
	DataPlaneState           string
	DataPlaneReason          string
	AuditWords               config.AuditWords
	Management               config.ManagementWords
	Errors                   config.ErrorCatalog
	TLSConfig                *tls.Config
	RequireClientCertificate bool
	contractOnce             sync.Once
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

func (server *Server) Handler() http.Handler {
	server.contractOnce.Do(func() {
		if server.Management.Paths.Groups == "" {
			server.Management, _ = config.LoadManagement()
		}
		server.Errors, _ = config.LoadErrorCatalog()
	})
	return http.HandlerFunc(server.handle)
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
	if server.RequireClientCertificate && (request.TLS == nil || len(request.TLS.PeerCertificates) == 0) {
		server.writeCatalogProblem(response, server.Management.Codes.MTLSRequired, requestID)
		return
	}
	_, authorized, authErr := server.authenticate(request.Context(), request.Header.Get("Authorization"))
	if authErr != nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	if !authorized {
		server.writeCatalogProblem(response, server.Management.Codes.BearerRequired, requestID)
		return
	}
	path := strings.TrimSuffix(request.URL.Path, "/")
	switch {
	case path == server.Management.Paths.Status && request.Method == http.MethodGet:
		server.mu.RLock()
		defer server.mu.RUnlock()
		readiness := map[string]any{"state": server.DataPlaneState}
		if server.DataPlaneState == server.Management.Statuses.NotReady {
			readiness[server.Management.JSON.Reason] = server.DataPlaneReason
		}
		writeJSON(response, 200, map[string]any{
			server.Management.JSON.Caddy: map[string]any{
				server.Management.JSON.Variant: server.CaddyVariant,
				server.Management.JSON.BuildID: server.CaddyBuildID,
				server.Management.JSON.Modules: server.CaddyModules,
			},
			server.Management.JSON.Drift:              false,
			server.Management.JSON.DataPlaneReadiness: readiness,
			server.Management.JSON.RequestID:          requestID,
		})
	case path == server.Management.Paths.Groups && request.Method == server.Management.Methods.Get:
		server.handleGroupList(response, request, requestID)
	case path == server.Management.Paths.Groups && request.Method == server.Management.Methods.Post:
		server.handleGroupCreate(response, request, requestID)
	case strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.HasSuffix(path, server.Management.Paths.GroupReleases) && request.Method == server.Management.Methods.Get:
		server.handleGroupReleases(response, request, path, requestID)
	case strings.HasPrefix(path, server.Management.Paths.GroupByID) && strings.Contains(path, server.Management.Paths.GroupReleases+server.Management.Paths.GroupIDSeparator) && request.Method == server.Management.Methods.Get:
		server.handleGroupRelease(response, request, path, requestID)
	case strings.HasPrefix(path, server.Management.Paths.GroupByID) && request.Method == server.Management.Methods.Get:
		server.handleGroupGet(response, request, path, requestID)
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
		writeJSON(response, 200, map[string]any{"id": operation.ID, "state": operation.State, "createdAt": operation.CreatedAt, server.Management.JSON.Result: operation.Result, server.Management.JSON.RequestID: requestID})
	default:
		writeProblem(response, 404, "not_found", "resource not found", requestID)
	}
}

func (server *Server) handleGroupList(response http.ResponseWriter, request *http.Request, requestID string) {
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	groups, err := server.GroupService.List(request.Context())
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(groups.Items))
	for _, group := range groups.Items {
		items = append(items, server.groupResponse(group))
	}
	writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.Items:     items,
		server.Management.JSON.RequestID: requestID,
	})
}

func (server *Server) handleGroupCreate(response http.ResponseWriter, request *http.Request, requestID string) {
	decoder := json.NewDecoder(request.Body)
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	if len(fields) != 2 {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	for key := range fields {
		if key != server.Management.JSON.ID && key != server.Management.JSON.IdempotencyKey {
			server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
			return
		}
	}
	var id, key string
	if json.Unmarshal(fields[server.Management.JSON.ID], &id) != nil || json.Unmarshal(fields[server.Management.JSON.IdempotencyKey], &key) != nil || len(key) < server.Management.Idempotency.KeyMin || len(key) > server.Management.Idempotency.KeyChars || !ascii(key) {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	validID, err := regexp.MatchString(server.Management.Paths.GroupIDPattern, id)
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	if !validID {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	group, err := server.GroupService.Create(request.Context(), id)
	if err != nil {
		var exists models.GroupAlreadyExists
		if errors.As(err, &exists) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupAlreadyExists, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	response.Header().Set(server.Management.Headers.Location, server.Management.Paths.GroupByID+id)
	result := server.groupResponse(group)
	result[server.Management.JSON.RequestID] = requestID
	writeJSON(response, http.StatusCreated, result)
}

func (server *Server) handleGroupGet(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	id := strings.TrimPrefix(path, server.Management.Paths.GroupByID)
	if id == "" || strings.Contains(id, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	group, err := server.GroupService.Get(request.Context(), id)
	if err != nil {
		var notFound models.GroupNotFound
		if errors.As(err, &notFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	result := server.groupResponse(group)
	result[server.Management.JSON.RequestID] = requestID
	writeJSON(response, http.StatusOK, result)
}

func (server *Server) handleGroupReleases(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	suffix := server.Management.Paths.GroupIDSeparator + server.Management.Paths.GroupReleases
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.GroupByID), suffix)
	if groupID == "" || strings.Contains(groupID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	limit := 0
	if rawLimit := request.URL.Query().Get(server.Management.JSON.Limit); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
			return
		}
		limit = parsed
	}
	page, err := server.GroupService.ListRevisions(request.Context(), groupID, request.URL.Query().Get(server.Management.JSON.Cursor), limit)
	if err != nil {
		var notFound models.GroupNotFound
		if errors.As(err, &notFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		var invalidPage models.GroupRevisionPageError
		if errors.As(err, &invalidPage) {
			server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, revision := range page.Items {
		items = append(items, map[string]any{
			server.Management.JSON.ID:              revision.ID,
			server.Management.JSON.GroupID:         revision.GroupID,
			server.Management.JSON.CaddyfileDigest: revision.CaddyfileDigest,
			server.Management.JSON.ArtifactDigest:  revision.ArtifactDigest,
			server.Management.JSON.CreatedAt:       revision.CreatedAt,
			server.Management.JSON.Actor:           revision.Actor,
		})
	}
	writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.Items:      items,
		server.Management.JSON.NextCursor: page.NextCursor,
		server.Management.JSON.RequestID:  requestID,
	})
}

func (server *Server) handleGroupRelease(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.GroupService.Store == nil || server.GroupService.ContentReader == nil {
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	suffix := server.Management.Paths.GroupIDSeparator + server.Management.Paths.GroupReleases + server.Management.Paths.GroupIDSeparator
	resource := strings.TrimPrefix(path, server.Management.Paths.GroupByID)
	groupID, revisionID, found := strings.Cut(resource, suffix)
	if !found || groupID == "" || revisionID == "" || strings.Contains(revisionID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	detail, err := server.GroupService.GetRevisionDetail(request.Context(), groupID, revisionID)
	if err != nil {
		var notFound models.GroupRevisionNotFound
		if errors.As(err, &notFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.RegistryUnavailable, requestID)
		return
	}
	revision := detail.Revision
	frontends := make([]map[string]any, 0, len(detail.Frontends))
	for _, frontend := range detail.Frontends {
		frontends = append(frontends, map[string]any{
			server.Management.JSON.ID:     frontend.ID,
			server.Management.JSON.Digest: frontend.Digest,
			server.Management.JSON.Files:  frontend.Files,
		})
	}
	writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.ID:              revision.ID,
		server.Management.JSON.GroupID:         revision.GroupID,
		server.Management.JSON.Caddyfile:       detail.Caddyfile,
		server.Management.JSON.CaddyfileDigest: revision.CaddyfileDigest,
		server.Management.JSON.ArtifactDigest:  revision.ArtifactDigest,
		server.Management.JSON.Frontends:       frontends,
		server.Management.JSON.CreatedAt:       revision.CreatedAt,
		server.Management.JSON.Actor:           revision.Actor,
		server.Management.JSON.RequestID:       requestID,
	})
}

func (server *Server) groupResponse(group models.Group) map[string]any {
	state := server.Management.Statuses.Ready
	if group.CurrentRevisionID == nil {
		state = server.Management.Statuses.Empty
	}
	return map[string]any{
		server.Management.JSON.ID:               group.ID,
		server.Management.JSON.Kind:             group.Kind,
		server.Management.JSON.Active:           group.Active,
		server.Management.JSON.CurrentRevision:  group.CurrentRevisionID,
		server.Management.JSON.PreviousRevision: group.PreviousRevisionID,
		server.Management.JSON.State:            state,
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

func (server *Server) writeCatalogProblem(response http.ResponseWriter, code, requestID string) {
	problem, exists := server.Errors.Lookup(code)
	if !exists {
		writeProblem(response, http.StatusInternalServerError, "config_invalid", "management error contract is invalid", requestID)
		return
	}
	writeProblem(response, problem.Status, problem.Code, problem.Detail, requestID)
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
func (server *Server) authenticate(ctx context.Context, value string) (string, bool, error) {
	if !strings.HasPrefix(value, "Bearer ") {
		return "", false, nil
	}
	key := strings.TrimPrefix(value, "Bearer ")
	if server.AccessService != nil {
		actor, valid, err := server.AccessService.Authenticate(ctx, key)
		return actor, valid, err
	}
	if server.Token != "" && key == server.Token {
		return server.AuditWords.Audit.Actors.StaticToken, true, nil
	}
	for _, account := range server.ServiceAccounts {
		if bcrypt.CompareHashAndPassword([]byte(account.KeyHash), []byte(key)) == nil {
			return account.ID, true, nil
		}
	}
	return "", false, nil
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
	_ = server.Audit.Record(ctx, record)
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
