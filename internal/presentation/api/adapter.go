// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Server struct {
	Token         string
	Config        string
	Revision      string
	Digest        string
	mu            sync.RWMutex
	operations    map[string]Operation
	audit         []Audit
	AdminSurfaces []AdminSurface
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
		writeJSON(response, http.StatusOK, map[string]any{"status": "ok", "requestId": requestID})
		return
	}
	if server.Token != "" && request.Header.Get("Authorization") != "Bearer "+server.Token {
		writeProblem(response, 401, "unauthorized", "management authentication required", requestID)
		return
	}
	path := strings.TrimSuffix(request.URL.Path, "/")
	switch {
	case path == "/api/status" && request.Method == http.MethodGet:
		server.mu.RLock()
		defer server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"revision": server.Revision, "digest": server.Digest, "listeners": []any{}, "upstreams": []any{}, "plugins": []any{}, "requestId": requestID})
	case path == "/api/config" && request.Method == http.MethodGet:
		server.mu.RLock()
		defer server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"revision": server.Revision, "digest": server.Digest, "yaml": redact(server.Config), "requestId": requestID})
	case path == "/api/config/validate" && request.Method == http.MethodPost:
		writeJSON(response, 200, map[string]any{"revision": server.Revision, "digest": server.Digest, "valid": true, "requestId": requestID})
	case path == "/api/audit" && request.Method == http.MethodGet:
		server.mu.RLock()
		defer server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"items": server.audit, "nextCursor": nil, "requestId": requestID})
	case path == "/api/plugins/admin-surfaces" && request.Method == http.MethodGet:
		server.mu.RLock()
		surfaces := append([]AdminSurface(nil), server.AdminSurfaces...)
		server.mu.RUnlock()
		writeJSON(response, 200, map[string]any{"items": surfaces, "requestId": requestID})
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
func randomID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(bytes)
}
func redact(value string) string { return strings.ReplaceAll(value, "secret:", "secret: ***") }
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
