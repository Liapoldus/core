package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminSurfacesEndpoint(t *testing.T) {
	server := &Server{AdminSurfaces: []AdminSurface{{Plugin: "forms-db", Namespace: "forms-db", Version: "v1", Title: "Forms", Capabilities: []string{"forms.list"}}}}
	recording := httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, httptest.NewRequest(http.MethodGet, "/api/plugins/admin-surfaces", nil))
	if recording.Code != http.StatusOK {
		t.Fatalf("status = %d", recording.Code)
	}
}
