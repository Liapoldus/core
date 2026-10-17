package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

const serviceSecretReferenceRequestMaximumBytes = 8 << 10

type serviceSecretReferenceCreateRequest struct {
	Reference string `json:"reference"`
	Purpose   string `json:"purpose"`
	Label     string `json:"label"`
}

func IsServiceSecretReferencePath(deps ServiceDependencies, path string) bool {
	_, _, collection, matched := serviceSecretReferencePath(deps, path)
	return matched && collection
}

func IsServiceSecretReferenceDetailPath(deps ServiceDependencies, path string) bool {
	_, _, collection, matched := serviceSecretReferencePath(deps, path)
	return matched && !collection
}

func serviceSecretReferencePath(deps ServiceDependencies, path string) (serviceID, referenceID string, collection, matched bool) {
	base := deps.Management.Paths.Services + deps.Management.Paths.ServiceIDSeparator
	separator := deps.Management.Paths.ServiceIDSeparator
	suffix := separator + deps.Management.Paths.ServiceSecretsSuffix
	if base == separator || deps.Management.Paths.Services == "" || separator == "" || deps.Management.Paths.ServiceSecretsSuffix == "" || !strings.HasPrefix(path, base) {
		return "", "", false, false
	}
	parts := strings.Split(strings.TrimPrefix(path, base), separator)
	if len(parts) != 2 && len(parts) != 3 || parts[0] == "" || parts[1] != deps.Management.Paths.ServiceSecretsSuffix {
		return "", "", false, false
	}
	if len(parts) == 2 {
		return parts[0], "", true, true
	}
	if parts[2] == "" || !models.ValidServiceSecretReferenceID(parts[2]) || suffix == "" {
		return "", "", false, false
	}
	return parts[0], parts[2], false, true
}

func ServiceSecretReferenceList(deps ServiceDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) {
	if deps.ServiceSecretReferences == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	serviceID, _, _, matched := serviceSecretReferencePath(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	items, err := deps.ServiceSecretReferences.List(request.Context(), serviceID, request.URL.Query().Get("purpose"))
	if err != nil {
		writeServiceSecretReferenceFailure(deps, response, requestID, err)
		return
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{"items": items})
}

func ServiceSecretReferenceCreate(deps ServiceDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.ServiceSecretReferences == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	serviceID, _, _, matched := serviceSecretReferencePath(deps, path)
	if !matched || !isJSONRequest(request, deps.Management.ContentTypes.JSON) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, serviceSecretReferenceRequestMaximumBytes))
	decoder.DisallowUnknownFields()
	var input serviceSecretReferenceCreateRequest
	if decoder.Decode(&input) != nil || !singleJSONDocument(decoder) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	reference := models.ServiceSecretReference{
		ID: input.Reference, ServiceID: serviceID, Purpose: input.Purpose, Label: input.Label,
		State: models.ServiceSecretReferenceActive,
	}
	if reference.Validate() != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	created, err := deps.ServiceSecretReferences.Create(request.Context(), reference, models.AuditRecord{
		Actor: actor, Action: deps.AuditWords.Audit.Actions.ServiceSecretCreate,
		Resource: serviceID + "/" + reference.ID, Result: deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
	})
	if err != nil {
		writeServiceSecretReferenceFailure(deps, response, requestID, err)
		return
	}
	deps.WriteJSON(response, http.StatusCreated, created)
}

func ServiceSecretReferenceRevoke(deps ServiceDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.ServiceSecretReferences == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	serviceID, referenceID, _, matched := serviceSecretReferencePath(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if err := deps.ServiceSecretReferences.Revoke(request.Context(), serviceID, referenceID, models.AuditRecord{
		Actor: actor, Action: deps.AuditWords.Audit.Actions.ServiceSecretRevoke,
		Resource: serviceID + "/" + referenceID, Result: deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
	}); err != nil {
		writeServiceSecretReferenceFailure(deps, response, requestID, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func writeServiceSecretReferenceFailure(deps ServiceDependencies, response http.ResponseWriter, requestID string, err error) {
	var notFound models.ServiceSecretReferenceNotFound
	var conflict models.ServiceSecretReferenceConflict
	var invalid models.ServiceSecretReferenceInvalid
	var unavailable models.ServiceSecretReferenceUnavailable
	switch {
	case errors.As(err, &notFound):
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.As(err, &conflict):
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
	case errors.As(err, &invalid):
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
	case errors.As(err, &unavailable):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	default:
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	}
}

func isJSONRequest(request *http.Request, jsonMediaType string) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mediaType, jsonMediaType)
}

func singleJSONDocument(decoder *json.Decoder) bool {
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}
