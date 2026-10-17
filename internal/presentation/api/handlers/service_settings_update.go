package handlers

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func ServiceSettingsUpdate(
	deps ServiceDependencies,
	response http.ResponseWriter,
	request *http.Request,
	service *application.ServiceConfigurationService,
	path, requestID, actor string,
) {
	if service == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get(deps.Management.Headers.ContentType))
	if err != nil || mediaType != deps.Management.ContentTypes.JSON {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	defer request.Body.Close()
	if service.MaximumPayloadBytes < 1 {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceConfigInvalid, requestID)
		return
	}
	input, err := decodeServiceSettings(request.Body, service.MaximumPayloadBytes)
	if err != nil {
		code := deps.Management.Codes.InvalidRequest
		var tooLarge models.ServiceConfigurationPayloadTooLarge
		if errors.As(err, &tooLarge) {
			code = deps.Management.Codes.ServiceConfigInvalid
		}
		deps.WriteCatalogProblem(response, code, requestID)
		return
	}
	expectedRevision, ok := parseStrongRevisionETag(request.Header.Get(deps.Management.Headers.IfMatch))
	if !ok {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	idempotencyKey := request.Header.Get(deps.Management.Idempotency.Key)
	keyLength := utf8.RuneCountInString(idempotencyKey)
	if idempotencyKey == "" || keyLength < deps.Management.Idempotency.KeyMin || keyLength > deps.Management.Idempotency.KeyMax {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	instanceID := strings.TrimPrefix(path, deps.Management.Paths.Services+deps.Management.Paths.ServiceIDSeparator)
	instanceID = strings.TrimSuffix(instanceID, deps.Management.Paths.ServiceSettingsSuffix)
	current, err := service.Current(request.Context(), instanceID)
	if err != nil {
		var missing models.ServiceConfigurationNotFound
		if errors.As(err, &missing) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceNotFound, requestID)
			return
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	operationID, err := newOperationID()
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	payload := models.OperationPayload{
		Version: service.PayloadVersion, Resource: instanceID, ExpectedRevision: expectedRevision,
		SchemaVersion: current.SchemaVersion,
	}
	payload.Digest = payload.ComputeDigest(input)
	operation := models.Operation{
		ID: operationID, Kind: deps.Management.OperationKinds.ServiceSettingsApply,
		State: deps.Management.Statuses.Pending, CreatedAt: time.Now().UTC(),
		RequestID: requestID, Actor: actor, Resource: instanceID,
	}
	command := application.ApplyServiceConfigurationCommand{
		InstanceID: instanceID, ExpectedRevision: expectedRevision,
		SchemaVersion: current.SchemaVersion, SettingsJSON: input,
		CandidateAudit: models.AuditRecord{
			Actor: actor, Action: deps.AuditWords.Audit.Actions.ServiceSettingsCandidate,
			Result: deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
		},
		AppliedAudit: models.AuditRecord{
			Actor: actor, Action: deps.AuditWords.Audit.Actions.ServiceSettingsApply,
			Result: deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
		},
		FailedAudit: models.AuditRecord{
			Actor: actor, Action: deps.AuditWords.Audit.Actions.ServiceSettingsApplyFailed,
			Result: deps.AuditWords.Audit.Results.Failed, RequestID: requestID,
		},
	}
	reserved, err := service.Submit(request.Context(), command, models.OperationReservation{
		Operation: operation, Scope: path, Key: idempotencyKey, RequestDigest: payload.Digest, Payload: &payload,
	})
	if err != nil {
		writeServiceSettingsFailure(deps, response, requestID, err)
		return
	}
	response.Header().Set(deps.Management.Headers.Location, deps.Management.Paths.Operations+deps.Management.Paths.ServiceIDSeparator+reserved.ID)
	deps.WriteJSON(response, http.StatusAccepted, map[string]any{
		deps.Management.JSON.OperationID: reserved.ID,
		deps.Management.JSON.State:       deps.Management.Statuses.Pending,
		deps.Management.JSON.RequestID:   requestID,
	})
}

func decodeServiceSettings(body io.Reader, maximumBytes int) ([]byte, error) {
	limited := io.LimitReader(body, int64(maximumBytes)+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, serviceSettingsRequestError{}
	}
	if len(raw) > maximumBytes {
		return nil, models.ServiceConfigurationPayloadTooLarge{}
	}
	if !utf8.Valid(raw) {
		return nil, serviceSettingsRequestError{}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoded, err := decodeUniqueJSON(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, serviceSettingsRequestError{}
	}
	root, ok := decoded.(map[string]any)
	if !ok || root == nil {
		return nil, serviceSettingsRequestError{}
	}
	return append([]byte(nil), raw...), nil
}

// serviceSettingsRequestError marks a malformed request payload: unreadable
// body, invalid UTF-8, trailing content, a non-object root, or duplicate object
// keys. It carries no detail because it is never surfaced to the client.
type serviceSettingsRequestError struct{}

func (serviceSettingsRequestError) Error() string { return "" }

func decodeUniqueJSON(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, serviceSettingsRequestError{}
			}
			if _, duplicate := object[key]; duplicate {
				return nil, serviceSettingsRequestError{}
			}
			value, err := decodeUniqueJSON(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueJSON(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		return array, nil
	default:
		return nil, serviceSettingsRequestError{}
	}
}

func parseStrongRevisionETag(value string) (int64, bool) {
	if value == "" || strings.HasPrefix(value, "W/") {
		return 0, false
	}
	quoted := strings.Trim(value, "\"")
	revision, err := strconv.ParseInt(quoted, 10, 64)
	return revision, err == nil && revision >= 0 && revisionETag(revision) == value
}

func newOperationID() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func writeServiceSettingsFailure(deps ServiceDependencies, response http.ResponseWriter, requestID string, err error) {
	var notFound models.ServiceConfigurationNotFound
	var conflict models.ServiceConfigurationConflict
	var idempotency models.IdempotencyConflict
	var unavailable models.ServiceConfigurationUnavailable
	var rejected models.ServiceConfigurationRejected
	var tooLarge models.ServiceConfigurationPayloadTooLarge
	switch {
	case errors.As(err, &notFound):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceNotFound, requestID)
	case errors.As(err, &conflict):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceRevisionConflict, requestID)
	case errors.As(err, &idempotency):
		deps.WriteCatalogProblem(response, deps.Management.Codes.IdempotencyConflict, requestID)
	case errors.As(err, &rejected):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceConfigInvalid, requestID)
	case errors.As(err, &tooLarge):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ServiceConfigInvalid, requestID)
	case errors.As(err, &unavailable):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	default:
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	}
}
