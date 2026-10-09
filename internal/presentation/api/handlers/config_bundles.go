package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/Liapoldus/core/internal/domain/models"
)

type ConfigBundleDependencies struct {
	WriteJSON           func(http.ResponseWriter, int, any)
	WriteProblem        func(http.ResponseWriter, int, string, string, string)
	WriteCatalogProblem func(http.ResponseWriter, string, string)
	Plan                func(context.Context, models.ConfigBundleRequest) (models.ConfigBundlePlan, error)
	Apply               func(context.Context, models.ConfigBundleRequest, string, string, string) (models.ConfigBundleApply, error)
}

// ConfigBundlePlan validates without creating durable state.
func ConfigBundlePlan(deps ConfigBundleDependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	var input models.ConfigBundleRequest
	if err := decodeBundle(request, &input); err != nil || deps.Plan == nil {
		deps.WriteCatalogProblem(response, "invalid_request", requestID)
		return
	}
	plan, err := deps.Plan(request.Context(), input)
	if err != nil {
		deps.WriteProblem(response, http.StatusUnprocessableEntity, "bundle_invalid", err.Error(), requestID)
		return
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{"plan": plan, "requestId": requestID})
}

// ConfigBundleApply creates the Core-owned operations for a validated bundle.
func ConfigBundleApply(deps ConfigBundleDependencies, response http.ResponseWriter, request *http.Request, requestID, actor string) {
	var input models.ConfigBundleRequest
	if err := decodeBundle(request, &input); err != nil || deps.Apply == nil {
		deps.WriteCatalogProblem(response, "invalid_request", requestID)
		return
	}
	key := request.Header.Get("Idempotency-Key")
	if key == "" {
		deps.WriteCatalogProblem(response, "invalid_request", requestID)
		return
	}
	result, err := deps.Apply(request.Context(), input, actor, requestID, key)
	if err != nil {
		deps.WriteProblem(response, http.StatusConflict, "bundle_apply_failed", err.Error(), requestID)
		return
	}
	deps.WriteJSON(response, http.StatusAccepted, map[string]any{"operations": result.OperationIDs, "requestId": requestID})
}

func decodeBundle(request *http.Request, target *models.ConfigBundleRequest) error {
	if request == nil {
		return errors.New("request required")
	}
	mediaType, _, mediaErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if request == nil || request.Body == nil || mediaErr != nil || mediaType != "application/json" {
		return errors.New("application/json body required")
	}
	defer request.Body.Close()
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing request content")
	}
	return nil
}
