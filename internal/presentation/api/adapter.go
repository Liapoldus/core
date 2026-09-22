// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"golang.org/x/crypto/bcrypt"
	"net/http"
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
	operations      map[string]Operation
	audit           []Audit
	AdminSurfaces   []AdminSurface
	AdminDispatcher *plugins.Dispatcher
	Listeners       []any
	Upstreams       []any
	Plugins         []any
	ValidateConfig  func(string) error
	ReloadConfig    func(context.Context, string) (Operation, error)
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

func (server *Server) Handler() http.Handler { return http.HandlerFunc(server.handle) }

func (server *Server) Listen(ctx context.Context, address string) error {
	httpServer := &http.Server{Addr: address, Handler: server.Handler()}
	result := make(chan error, 1)
	go func() { result <- httpServer.ListenAndServe() }()
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
		server.mu.Unlock()
		writeJSON(response, 202, map[string]any{"revision": server.Revision, "digest": server.Digest, "requestId": requestID})
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
	case path == "/api/config/reload" && request.Method == http.MethodPost:
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
	case path == "/api/audit" && request.Method == http.MethodGet:
		server.mu.RLock()
		defer server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"items": server.audit, "nextCursor": nil, "requestId": requestID})
	case path == "/metrics" && request.Method == http.MethodGet:
		response.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintln(response, "# HELP liapoldus_management_requests_total Management API requests")
		_, _ = fmt.Fprintln(response, "# TYPE liapoldus_management_requests_total counter")
		_, _ = fmt.Fprintln(response, "liapoldus_management_requests_total 1")
	case path == "/api/plugins/admin-surfaces" && request.Method == http.MethodGet:
		server.mu.RLock()
		surfaces := append([]AdminSurface(nil), server.AdminSurfaces...)
		server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"items": surfaces, "requestId": requestID})
	case strings.HasPrefix(path, "/api/plugins/") && strings.Contains(path, "/admin/pages/") && (request.Method == http.MethodGet || request.Method == http.MethodPost):
		server.handlePluginAdmin(response, request, path, requestID)
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
