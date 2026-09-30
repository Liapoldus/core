package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Liapoldus/core/internal/domain/models"
)

func Healthz(deps ManagementDependencies, response http.ResponseWriter, request *http.Request, requestID string) bool {
	if request.URL.Path != deps.Management.Paths.Healthz {
		return false
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		deps.WriteProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "health endpoint accepts GET and HEAD", requestID)
		return true
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Status:    deps.Management.Statuses.OK,
		deps.Management.JSON.RequestID: requestID,
	})
	return true
}

func Readiness(deps ManagementDependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	state, reason := deps.DataPlaneState, deps.DataPlaneReason
	if deps.DataPlaneReadiness != nil {
		state, reason = deps.DataPlaneReadiness(request.Context())
	}
	readiness := map[string]any{deps.Management.JSON.State: state}
	if state == deps.Management.Statuses.NotReady {
		readiness[deps.Management.JSON.Reason] = reason
	}
	// Drift is derived from durable observations, never assumed. When Core
	// cannot read its own observation store it reports drift instead of
	// claiming no drift, because a Core that cannot tell is not a Core that
	// is in sync.
	drift := true
	if deps.DataPlaneDrift != nil {
		drift = deps.DataPlaneDrift(request.Context())
	}
	deps.WriteJSON(response, 200, map[string]any{
		deps.Management.JSON.Drift:              drift,
		deps.Management.JSON.DataPlaneReadiness: readiness,
		deps.Management.JSON.RequestID:          requestID,
	})
}

func AuditList(deps ManagementDependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	if deps.Audit == nil {
		deps.WriteJSON(response, http.StatusOK, map[string]any{
			deps.Management.JSON.Items:      []models.AuditRecord{},
			deps.Management.JSON.NextCursor: nil,
			deps.Management.JSON.RequestID:  requestID,
		})
		return
	}
	limit := deps.Management.Pagination.LimitDefault
	if rawLimit := request.URL.Query().Get(deps.Management.JSON.Limit); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return
		}
		limit = parsed
	}
	page, err := deps.Audit.Records(request.Context(), request.URL.Query().Get(deps.Management.JSON.Cursor), limit)
	if err != nil {
		var pageError models.AuditPageError
		if errors.As(err, &pageError) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return
		}
		deps.WriteProblem(response, http.StatusServiceUnavailable, deps.AuditWords.Audit.StorageUnavailable.Code, deps.AuditWords.Audit.StorageUnavailable.Detail, requestID)
		return
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Items:      page.Items,
		deps.Management.JSON.NextCursor: page.NextCursor,
		deps.Management.JSON.RequestID:  requestID,
	})
}

func OperationGet(deps ManagementDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) {
	id := strings.TrimPrefix(path, deps.Management.Paths.Operations+"/")
	if deps.Operations.Store == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	operation, err := deps.Operations.Get(request.Context(), id)
	if err != nil {
		var notFound models.OperationNotFound
		if errors.As(err, &notFound) {
			deps.WriteProblem(response, http.StatusNotFound, deps.Management.Codes.OperationNotFound, deps.Management.Diagnostics.OperationNotFound, requestID)
			return
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	result := map[string]any{
		deps.Management.JSON.ID:         operation.ID,
		deps.Management.JSON.Kind:       operation.Kind,
		deps.Management.JSON.State:      operation.State,
		deps.Management.JSON.ResourceID: operation.Resource,
		deps.Management.JSON.CreatedAt:  operation.CreatedAt,
		deps.Management.JSON.RequestID:  operation.RequestID,
	}
	if operation.UpdatedAt != nil {
		result[deps.Management.JSON.UpdatedAt] = *operation.UpdatedAt
	}
	if operation.ErrorCode != "" {
		result[deps.Management.JSON.ErrorCode] = operation.ErrorCode
	}
	deps.WriteJSON(response, http.StatusOK, result)
}
