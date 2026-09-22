package api

import (
	"context"
	"encoding/json"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testAdminPlugin struct{}

func (testAdminPlugin) Dispatch(_ context.Context, request plugins.RequestContext) (plugins.ResponseAction, error) {
	body, _ := json.Marshal(map[string]string{"page": request.Page, "action": request.Action})
	return plugins.ResponseAction{Status: http.StatusOK, Body: body}, nil
}

func TestTLSOperationUsesTypedIssuerBoundary(t *testing.T) {
	server := &Server{RenewTLS: func(_ context.Context, issuer, key string) (Operation, error) {
		if issuer != "acme" || key != "0123456789abcdef" {
			t.Fatalf("issuer=%q key=%q", issuer, key)
		}
		return Operation{ID: "op-renew", State: "pending", CreatedAt: time.Now()}, nil
	}}
	recording := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tls/acme/renew", nil)
	request.Header.Set("Idempotency-Key", "0123456789abcdef")
	server.Handler().ServeHTTP(recording, request)
	if recording.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %s", recording.Code, recording.Body.String())
	}
	if !strings.Contains(recording.Body.String(), `"operationId":"op-renew"`) {
		t.Fatalf("body = %s", recording.Body.String())
	}
}

func TestTLSOperationRejectsInvalidIdempotencyKey(t *testing.T) {
	server := &Server{RenewTLS: func(context.Context, string, string) (Operation, error) {
		t.Fatal("issuer must not be called")
		return Operation{}, nil
	}}
	recording := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tls/acme/renew", nil)
	request.Header.Set("Idempotency-Key", "short")
	server.Handler().ServeHTTP(recording, request)
	if recording.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recording.Code)
	}
}

func TestAdminSurfacesEndpoint(t *testing.T) {
	server := &Server{AdminSurfaces: []AdminSurface{{Plugin: "forms-db", Namespace: "forms-db", Version: "v1", Title: "Forms", Capabilities: []string{"forms.list"}}}}
	recording := httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, httptest.NewRequest(http.MethodGet, "/api/plugins/admin-surfaces", nil))
	if recording.Code != http.StatusOK {
		t.Fatalf("status = %d", recording.Code)
	}
}

func TestManagementResourceListsAndPagination(t *testing.T) {
	server := &Server{Listeners: []any{"a", "b"}, Upstreams: []any{"u"}, Sites: []any{"s"}, Plugins: []any{"p"}, operations: map[string]Operation{"op": {ID: "op", State: "pending"}}}
	for _, path := range []string{"/api/listeners", "/api/upstreams", "/api/sites", "/api/plugins", "/api/operations"} {
		recording := httptest.NewRecorder()
		server.Handler().ServeHTTP(recording, httptest.NewRequest(http.MethodGet, path+"?limit=1", nil))
		if recording.Code != http.StatusOK {
			t.Fatalf("%s: got %d", path, recording.Code)
		}
	}
}

func TestHealthMethodGate(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	recording := httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, request)
	if recording.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recording.Code, http.StatusMethodNotAllowed)
	}
}

func TestManagementPluginRestartOperation(t *testing.T) {
	server := &Server{RestartPlugin: func(context.Context, string) (Operation, error) { return Operation{ID: "op-1", State: "accepted"}, nil }}
	recording := httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, httptest.NewRequest(http.MethodPost, "/api/plugins/forms/restart", nil))
	if recording.Code != http.StatusAccepted {
		t.Fatalf("got %d", recording.Code)
	}
}

func TestPluginAdminDispatchBoundary(t *testing.T) {
	dispatcher := plugins.NewDispatcher()
	if err := dispatcher.Register("forms", testAdminPlugin{}); err != nil {
		t.Fatal(err)
	}
	server := &Server{AdminDispatcher: dispatcher}
	recording := httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, httptest.NewRequest(http.MethodPost, "/api/plugins/forms/admin/pages/submissions/query", nil))
	if recording.Code != http.StatusOK {
		t.Fatalf("status = %d", recording.Code)
	}
	if !strings.Contains(recording.Body.String(), `"page":"submissions"`) {
		t.Fatalf("body = %s", recording.Body.String())
	}
}
