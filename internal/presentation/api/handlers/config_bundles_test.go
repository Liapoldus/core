package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func TestConfigBundlePlanIsReadOnlyAndDecodesOpaqueSettings(t *testing.T) {
	var planned models.ConfigBundleRequest
	deps := ConfigBundleDependencies{
		WriteJSON:           func(response http.ResponseWriter, status int, value any) { response.WriteHeader(status) },
		WriteProblem:        func(response http.ResponseWriter, status int, _, _, _ string) { response.WriteHeader(status) },
		WriteCatalogProblem: func(response http.ResponseWriter, _, _ string) { response.WriteHeader(http.StatusBadRequest) },
		Plan: func(_ context.Context, request models.ConfigBundleRequest) (models.ConfigBundlePlan, error) {
			planned = request
			return models.ConfigBundlePlan{Valid: true, Digest: request.Bundle.Digest}, nil
		},
	}
	body := `{"project":{"id":"demo","revision":"abc"},"bundle":{"schemaVersion":"core-config/v2","digest":"sha256:x","services":[{"id":"forms","settings":{"opaque":true}}]},"target":{"environment":"local"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/config-bundles/plan", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ConfigBundlePlan(deps, response, request, "req-1")
	if response.Code != http.StatusOK || len(planned.Bundle.Services) != 1 || string(planned.Bundle.Services[0].Settings) != `{"opaque":true}` {
		t.Fatalf("status=%d planned=%+v", response.Code, planned)
	}
}

func TestConfigBundleApplyRequiresIdempotencyKey(t *testing.T) {
	called := false
	deps := ConfigBundleDependencies{
		WriteJSON:           func(response http.ResponseWriter, status int, value any) { response.WriteHeader(status) },
		WriteProblem:        func(response http.ResponseWriter, status int, _, _, _ string) { response.WriteHeader(status) },
		WriteCatalogProblem: func(response http.ResponseWriter, _, _ string) { response.WriteHeader(http.StatusBadRequest) },
		Apply: func(context.Context, models.ConfigBundleRequest, string, string, string) (models.ConfigBundleApply, error) {
			called = true
			return models.ConfigBundleApply{OperationIDs: []string{"op"}}, nil
		},
	}
	body := `{"project":{"id":"demo","revision":"abc"},"bundle":{"schemaVersion":"core-config/v2","digest":"sha256:x"},"target":{"environment":"local"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/config-bundles/apply", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	ConfigBundleApply(deps, response, request, "req-1", "actor")
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("status=%d called=%v", response.Code, called)
	}
}
