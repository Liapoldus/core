package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
)

func ServiceKeyList(deps Dependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	if deps.AccessService == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	metadata, err := deps.AccessService.Metadata(request.Context())
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(metadata))
	for _, item := range metadata {
		items = append(items, map[string]any{
			deps.Management.JSON.ID:        item.ID,
			deps.Management.JSON.Name:      item.Name,
			deps.Management.JSON.Role:      item.Role,
			deps.Management.JSON.CreatedAt: item.CreatedAt,
			deps.Management.JSON.ExpiresAt: item.ExpiresAt,
			deps.Management.JSON.RevokedAt: item.RevokedAt,
		})
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Items:     items,
		deps.Management.JSON.RequestID: requestID,
	})
}

func ServiceKeyCreate(deps Dependencies, response http.ResponseWriter, request *http.Request, requestID, actor string) {
	if deps.AccessService == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	fields := make(map[string]json.RawMessage)
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&fields); err != nil || len(fields) != 1 {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	rawName, exists := fields[deps.Management.JSON.Name]
	if !exists {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	var name string
	if err := json.Unmarshal(rawName, &name); err != nil || utf8.RuneCountInString(name) < deps.Management.ServiceKeys.NameMinLength || utf8.RuneCountInString(name) > deps.Management.ServiceKeys.NameMaxLength {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	words, err := config.LoadCLI()
	if err != nil || words.ServiceKey.KeyBytes < 1 || words.ServiceKey.HashCost < 1 || words.ServiceKey.RolePlatformAdmin == "" || deps.Management.ServiceKeys.CreatedStatus < 1 {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	id, token, verifier, err := deps.GenerateServiceKey(words.ServiceKey.KeyBytes, words.ServiceKey.HashCost)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	defer clear(verifier)
	record := models.AuditRecord{
		Actor: actor, Action: deps.AuditWords.Audit.Actions.ServiceKeyCreate,
		Resource: deps.AuditWords.Audit.Resources.ServiceKeys,
		Result:   deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
	}
	if err := deps.AccessService.Create(request.Context(), models.ServiceKey{
		ID: id, Name: name, Verifier: verifier, Role: words.ServiceKey.RolePlatformAdmin,
	}, record); err != nil {
		var auditFailure models.AuditAppendError
		if errors.As(err, &auditFailure) {
			deps.WriteProblem(response, http.StatusServiceUnavailable, deps.AuditWords.Audit.StorageUnavailable.Code, deps.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	deps.WriteJSON(response, deps.Management.ServiceKeys.CreatedStatus, map[string]any{
		deps.Management.JSON.ID:        id,
		deps.Management.JSON.Name:      name,
		deps.Management.JSON.Role:      words.ServiceKey.RolePlatformAdmin,
		deps.Management.JSON.Token:     token,
		deps.Management.JSON.RequestID: requestID,
	})
}
