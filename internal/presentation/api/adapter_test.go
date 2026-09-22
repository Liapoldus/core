package api

import (
	"context"
	"encoding/json"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"golang.org/x/crypto/bcrypt"
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

func TestConfigPutUpdatesDigestAtomically(t *testing.T) {
	server := &Server{Revision: "rev-1", Config: "old", Digest: "old-digest"}
	recording := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(`{"yaml":"new"}`))
	request.Header.Set("If-Match", "rev-1")
	server.Handler().ServeHTTP(recording, request)
	if recording.Code != http.StatusAccepted || server.Digest == "old-digest" || server.Config != "new" || server.Revision == "rev-1" {
		t.Fatalf("status=%d config=%q revision=%q digest=%q", recording.Code, server.Config, server.Revision, server.Digest)
	}
}

func TestConfigReloadStoresOperation(t *testing.T) {
	server := &Server{Revision: "rev-1", ReloadConfig: func(_ context.Context, revision string) (Operation, error) {
		if revision != "rev-1" {
			t.Fatalf("revision=%q", revision)
		}
		return Operation{ID: "op-reload", State: "accepted"}, nil
	}}
	recording := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/config/reload", nil)
	request.Header.Set("If-Match", "rev-1")
	server.Handler().ServeHTTP(recording, request)
	if recording.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recording.Code, recording.Body.String())
	}
	recording = httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, httptest.NewRequest(http.MethodGet, "/api/operations/op-reload", nil))
	if recording.Code != http.StatusOK || !strings.Contains(recording.Body.String(), "op-reload") {
		t.Fatalf("status=%d body=%s", recording.Code, recording.Body.String())
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

func TestServiceAccountBearerAuthentication(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("account-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{ServiceAccounts: []models.ServiceAccount{{ID: "operator", KeyHash: string(hash)}}}
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Authorization", "Bearer account-secret")
	recording := httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, request)
	if recording.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recording.Code, recording.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.Header.Set("Authorization", "Bearer wrong-secret")
	recording = httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, request)
	if recording.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recording.Code)
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

func TestSitePublishUsesTypedRegistryBoundary(t *testing.T) {
	calls := 0
	server := &Server{PublishSite: func(_ context.Context, site, source, key string) (Operation, error) {
		calls++
		if site != "blog" || source != "/incoming/blog" || key != "1234567890abcdef" {
			t.Fatalf("site=%q source=%q key=%q", site, source, key)
		}
		return Operation{ID: "op-publish", State: "accepted"}, nil
	}}
	recording := httptest.NewRecorder()
	body := strings.NewReader(`{"source":"/incoming/blog","idempotencyKey":"1234567890abcdef"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/sites/blog/publish", body)
	server.Handler().ServeHTTP(recording, request)
	if recording.Code != http.StatusCreated || !strings.Contains(recording.Body.String(), "op-publish") {
		t.Fatalf("status=%d body=%s", recording.Code, recording.Body.String())
	}
	recording = httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, httptest.NewRequest(http.MethodPost, "/api/sites/blog/publish", strings.NewReader(`{"source":"/incoming/blog","idempotencyKey":"1234567890abcdef"}`)))
	if calls != 1 || !strings.Contains(recording.Body.String(), "op-publish") {
		t.Fatalf("idempotency calls=%d body=%s", calls, recording.Body.String())
	}
	recording = httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, httptest.NewRequest(http.MethodGet, "/api/operations", nil))
	if recording.Code != http.StatusOK || !strings.Contains(recording.Body.String(), "op-publish") {
		t.Fatalf("operations status=%d body=%s", recording.Code, recording.Body.String())
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
