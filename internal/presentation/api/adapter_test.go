package api

import (
	"context"
	"encoding/json"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testAdminPlugin struct{}

func (testAdminPlugin) Dispatch(_ context.Context, request plugins.RequestContext) (plugins.ResponseAction, error) {
	body, _ := json.Marshal(map[string]string{"page": request.Page, "action": request.Action})
	return plugins.ResponseAction{Status: http.StatusOK, Body: body}, nil
}

func TestAdminSurfacesEndpoint(t *testing.T) {
	server := &Server{AdminSurfaces: []AdminSurface{{Plugin: "forms-db", Namespace: "forms-db", Version: "v1", Title: "Forms", Capabilities: []string{"forms.list"}}}}
	recording := httptest.NewRecorder()
	server.Handler().ServeHTTP(recording, httptest.NewRequest(http.MethodGet, "/api/plugins/admin-surfaces", nil))
	if recording.Code != http.StatusOK {
		t.Fatalf("status = %d", recording.Code)
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
