package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServiceRoutesRetirePluginPaths(t *testing.T) {
	server := &Server{Token: "fixture-token", Services: []any{map[string]any{"id": "neutral-instance"}}, ServiceIDField: "id"}
	handler := server.Handler()
	for _, path := range []string{
		"/api/plugins", "/api/plugins/neutral-instance", "/api/plugins/neutral-instance/settings",
		"/api/plugins/neutral-instance/rollback", "/api/plugins/neutral-instance/replicas/register",
		"/api/plugins/neutral-instance/manifest", "/api/plugins/neutral-instance/schema", "/api/plugins/neutral-instance/status",
		"/api/plugins/admin-surfaces", "/api/plugins/neutral-instance/admin/pages/status/query",
		"/api/plugins/neutral-instance/admin/pages/status/actions/update", "/api/plugins/neutral-instance/rollouts",
		"/api/plugin-links", "/api/plugin-links/caller/target",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			request := httptest.NewRequest(method, path, nil)
			request.Header.Set("Authorization", "Bearer fixture-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("%s %s: %d", method, path, response.Code)
			}
			if response.Header().Get("Location") != "" {
				t.Fatalf("retired path redirects: %s", path)
			}
		}
	}
	request := httptest.NewRequest(http.MethodGet, server.Management.Paths.Services, nil)
	request.Header.Set("Authorization", "Bearer fixture-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("service collection: %d %s", response.Code, response.Body.String())
	}
}
