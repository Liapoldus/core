package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// IsPluginRollbackPath reports whether path addresses POST /plugins/{pluginId}/rollback.
func IsPluginRollbackPath(deps PluginDependencies, path string) bool {
	_, matched := pluginRollbackInstanceID(deps, path)
	return matched
}

func pluginRollbackInstanceID(deps PluginDependencies, path string) (string, bool) {
	separator := deps.Management.Paths.PluginIDSeparator
	suffix := deps.Management.Paths.PluginRollbackSuffix
	if separator == "" || suffix == "" || deps.Management.Paths.Plugins == "" {
		return "", false
	}
	prefix := deps.Management.Paths.Plugins + separator
	resource := strings.TrimPrefix(path, prefix)
	if resource == path || !strings.HasSuffix(resource, separator+suffix) {
		return "", false
	}
	instanceID := strings.TrimSuffix(resource, separator+suffix)
	return instanceID, instanceID != "" && !strings.Contains(instanceID, separator)
}

// PluginSettingsRollback swaps the durable active and previous configuration
// generations and then notifies replicas through an ordinary REST Reload. The
// request never carries plugin configuration.
func PluginSettingsRollback(
	deps PluginDependencies,
	response http.ResponseWriter,
	request *http.Request,
	service *application.PluginConfigurationService,
	path, requestID, actor string,
) {
	if service == nil || service.Store == nil || service.Applier == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	instanceID, matched := pluginRollbackInstanceID(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "plugin resource not found", requestID)
		return
	}
	expectedRevision, ok := parseStrongRevisionETag(request.Header.Get(deps.Management.Headers.IfMatch))
	if !ok || expectedRevision == 0 {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	idempotencyKey := request.Header.Get(deps.Management.Idempotency.Key)
	keyLength := utf8.RuneCountInString(idempotencyKey)
	if idempotencyKey == "" || keyLength < deps.Management.Idempotency.KeyMin || keyLength > deps.Management.Idempotency.KeyMax {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	if deps.Operations.Store == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	current, err := service.Current(request.Context(), instanceID)
	if err != nil {
		writePluginSettingsFailure(deps, response, requestID, err)
		return
	}
	payload := models.OperationPayload{
		Version: service.PayloadVersion, Resource: instanceID,
		ExpectedRevision: expectedRevision, SchemaVersion: current.SchemaVersion,
	}
	payload.Digest = payload.ComputeDigest(nil)
	operationID, err := newOperationID()
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	reservation := models.OperationReservation{
		Operation: models.Operation{
			ID: operationID, Kind: deps.Management.OperationKinds.PluginSettingsRollback,
			State: deps.Management.Statuses.Pending, CreatedAt: time.Now().UTC(),
			RequestID: requestID, Actor: actor, Resource: instanceID,
		},
		Scope: path, Key: idempotencyKey, RequestDigest: payload.Digest, Payload: &payload,
	}
	operation, claimed, err := deps.Operations.Reserve(request.Context(), reservation)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	if !claimed {
		writePluginRollbackAccepted(deps, response, operation.ID, operation.State, path, requestID)
		return
	}
	// Serialize the pointer swap and its Reload with configuration application.
	// Otherwise a rollback can race a just-promoted generation and apply an older
	// snapshot while the concurrent Apply is still finishing.
	application.SnapshotActivationLock.Lock()
	defer application.SnapshotActivationLock.Unlock()
	restored, registered, err := restorePreviousConfiguration(service, request, operation.ID, instanceID, expectedRevision,
		models.AuditRecord{
			Actor: actor, Action: deps.AuditWords.Audit.Actions.PluginSettingsRollback,
			Resource: instanceID, Result: deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
		})
	if err != nil {
		failPluginRollback(deps, response, request.Context(), operation.ID, requestID, err)
		return
	}
	if err := deps.Operations.Transition(request.Context(), operation.ID,
		deps.Management.Statuses.Pending, deps.Management.Statuses.Running, ""); err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	var applyErr error
	if registered {
		applyErr = service.ApplyRestoredConfiguration(request.Context(), operation.ID, restored)
	} else {
		applyErr = service.Applier.ApplyConfiguration(request.Context(), instanceID,
			strconv.FormatInt(restored.Revision, 10), restored.SettingsJSON)
	}
	if applyErr != nil {
		if registered {
			// Once the active pointer and frozen cohort are committed, this is a
			// roll-forward operation. Keep it recoverable instead of failing a
			// partially applied generation and leaving an open rollout barrier.
			writePluginRollbackAccepted(deps, response, operation.ID, deps.Management.Statuses.Running, path, requestID)
			return
		}
		_ = deps.Operations.Transition(request.Context(), operation.ID,
			deps.Management.Statuses.Running, deps.Management.Statuses.Failed, deps.Management.Codes.ActivationFailed)
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginUnavailable, requestID)
		return
	}
	if err := deps.Operations.Transition(request.Context(), operation.ID,
		deps.Management.Statuses.Running, deps.Management.Statuses.Succeeded, ""); err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	writePluginRollbackAccepted(deps, response, operation.ID, deps.Management.Statuses.Succeeded, path, requestID)
}

func failPluginRollback(
	deps PluginDependencies,
	response http.ResponseWriter,
	ctx context.Context, operationID, requestID string, err error,
) {
	var notFound models.PluginConfigurationNotFound
	var conflict models.PluginConfigurationConflict
	code := deps.Management.Codes.ManagementUnavailable
	switch {
	case errors.As(err, &notFound):
		code = deps.Management.Codes.PluginNotFound
	case errors.As(err, &conflict):
		code = deps.Management.Codes.PluginRevisionConflict
	}
	_ = deps.Operations.Transition(ctx, operationID, deps.Management.Statuses.Pending, deps.Management.Statuses.Failed, code)
	deps.WriteCatalogProblem(response, code, requestID)
}

func restorePreviousConfiguration(
	service *application.PluginConfigurationService,
	request *http.Request,
	operationID, instanceID string, expectedRevision int64,
	audit models.AuditRecord,
) (models.PluginConfigurationRevision, bool, error) {
	return service.RestorePreviousConfiguration(request.Context(), operationID, instanceID, expectedRevision, audit)
}

func writePluginRollbackAccepted(deps PluginDependencies, response http.ResponseWriter, operationID, state, path, requestID string) {
	response.Header().Set(deps.Management.Headers.Location, deps.Management.Paths.Operations+deps.Management.Paths.PluginIDSeparator+operationID)
	deps.WriteJSON(response, http.StatusAccepted, map[string]any{
		deps.Management.JSON.OperationID: operationID,
		deps.Management.JSON.State:       state,
		deps.Management.JSON.RequestID:   requestID,
	})
}
