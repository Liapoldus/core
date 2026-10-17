package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	maxOperationResolveBodyBytes  = 16 << 10
	maxOperationResolveKeys       = 64
	maxOperationResolveKeyBytes   = 512
	maxOperationResolveScopeBytes = 1024
)

type operationResolveRequest struct {
	Scope string   `json:"scope"`
	Keys  []string `json:"keys"`
}

// OperationResolve returns only safe operation metadata owned by the
// authenticated actor. It lets a caller recover accepted work after losing
// the HTTP response without persisting configuration payloads locally.
func OperationResolve(deps ManagementDependencies, response http.ResponseWriter, request *http.Request, requestID, actor string) {
	if deps.Operations.Store == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	var input operationResolveRequest
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, maxOperationResolveBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || !validOperationResolveRequest(input) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	items := make([]map[string]any, 0, len(input.Keys))
	for _, key := range input.Keys {
		operation, found, err := deps.Operations.FindByIdempotencyKey(request.Context(), actor, input.Scope, key)
		if err != nil {
			deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
			return
		}
		if !found {
			continue
		}
		item := map[string]any{
			deps.Management.JSON.ID:         operation.ID,
			deps.Management.JSON.State:      operation.State,
			deps.Management.JSON.ResourceID: operation.Resource,
		}
		if operation.ErrorCode != "" {
			item[deps.Management.JSON.ErrorCode] = operation.ErrorCode
		}
		items = append(items, item)
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Items:     items,
		deps.Management.JSON.RequestID: requestID,
	})
}

func validOperationResolveRequest(input operationResolveRequest) bool {
	if strings.TrimSpace(input.Scope) != input.Scope || input.Scope == "" || len(input.Scope) > maxOperationResolveScopeBytes ||
		len(input.Keys) == 0 || len(input.Keys) > maxOperationResolveKeys {
		return false
	}
	seen := make(map[string]struct{}, len(input.Keys))
	for _, key := range input.Keys {
		if strings.TrimSpace(key) != key || key == "" || len(key) > maxOperationResolveKeyBytes {
			return false
		}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}
