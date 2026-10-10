package handlers

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func IsTrafficRolloutCollectionPath(deps PluginDependencies, path string) bool {
	_, ok := trafficRolloutInstanceID(deps, path)
	return ok
}

func IsTrafficRolloutApprovalPath(deps PluginDependencies, path string) bool {
	_, _, _, ok := trafficRolloutApprovalPath(deps, path)
	return ok
}

func IsTrafficRolloutDetailPath(deps PluginDependencies, path string) bool {
	prefix := deps.Management.Paths.Plugins + deps.Management.Paths.PluginIDSeparator
	resource := strings.TrimPrefix(path, prefix)
	if resource == path {
		return false
	}
	instanceID, rolloutID, found := strings.Cut(resource, deps.TrafficRolloutAPI.CollectionSuffix+deps.TrafficRolloutAPI.RolloutIDSeparator)
	return found && instanceID != "" && rolloutID != "" && !strings.ContainsAny(instanceID+rolloutID, "/\\")
}

func TrafficRolloutGet(deps PluginDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) {
	if request.Method != deps.Management.Methods.Get || deps.TrafficRollouts == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	prefix := deps.Management.Paths.Plugins + deps.Management.Paths.PluginIDSeparator
	resource := strings.TrimPrefix(path, prefix)
	instanceID, rolloutID, _ := strings.Cut(resource, deps.TrafficRolloutAPI.CollectionSuffix+deps.TrafficRolloutAPI.RolloutIDSeparator)
	record, err := deps.TrafficRollouts.Get(request.Context(), rolloutID)
	if err != nil || record.InstanceID != instanceID {
		writeTrafficRolloutFailure(deps, response, requestID, models.TrafficRolloutNotFound{})
		return
	}
	stages := make([]map[string]any, len(record.Stages))
	for index, stage := range record.Stages {
		stages[index] = map[string]any{"id": stage.Stage.ID, "candidateWeightPercent": stage.Stage.CandidateWeightPercent,
			"minimumObservationSeconds": stage.Stage.MinimumObservationSeconds, "requireManualApproval": stage.Stage.RequireManualApproval,
			"state": stage.State}
	}
	response.Header().Set(deps.Management.Headers.ETag, revisionETag(record.Revision))
	deps.WriteJSON(response, http.StatusOK, map[string]any{"id": record.ID, "pluginId": record.InstanceID,
		"generation": record.Generation, "revision": record.Revision, "releaseSha256": record.ReleaseSHA256,
		"state": record.State, "activeStageIndex": record.ActiveStageIndex, "stages": stages})
}

func trafficRolloutApprovalPath(deps PluginDependencies, path string) (string, string, string, bool) {
	prefix := deps.Management.Paths.Plugins + deps.Management.Paths.PluginIDSeparator
	if prefix == deps.Management.Paths.PluginIDSeparator || deps.TrafficRolloutAPI.CollectionSuffix == "" ||
		deps.TrafficRolloutAPI.ApprovalStageSegment == "" || deps.TrafficRolloutAPI.ApprovalSuffix == "" {
		return "", "", "", false
	}
	resource := strings.TrimPrefix(path, prefix)
	if resource == path {
		return "", "", "", false
	}
	instanceID, remainder, found := strings.Cut(resource, deps.TrafficRolloutAPI.CollectionSuffix+"/")
	if !found || instanceID == "" || strings.Contains(instanceID, deps.Management.Paths.PluginIDSeparator) {
		return "", "", "", false
	}
	rolloutID, stagePart, found := strings.Cut(remainder, deps.TrafficRolloutAPI.ApprovalStageSegment)
	if !found || rolloutID == "" {
		return "", "", "", false
	}
	stageID, suffix, found := strings.Cut(stagePart, deps.TrafficRolloutAPI.ApprovalSuffix)
	if !found || stageID == "" || suffix != "" || strings.ContainsAny(rolloutID+stageID, "/\\") {
		return "", "", "", false
	}
	return instanceID, rolloutID, stageID, true
}

func trafficRolloutInstanceID(deps PluginDependencies, path string) (string, bool) {
	prefix := deps.Management.Paths.Plugins + deps.Management.Paths.PluginIDSeparator
	suffix := deps.TrafficRolloutAPI.CollectionSuffix
	if prefix == deps.Management.Paths.PluginIDSeparator || suffix == "" {
		return "", false
	}
	resource := strings.TrimPrefix(path, prefix)
	if resource == path || !strings.HasSuffix(resource, suffix) {
		return "", false
	}
	instanceID := strings.TrimSuffix(resource, suffix)
	return instanceID, instanceID != "" && !strings.Contains(instanceID, deps.Management.Paths.PluginIDSeparator)
}

func TrafficRolloutCreate(
	deps PluginDependencies,
	response http.ResponseWriter,
	request *http.Request,
	path, requestID, actor string,
) {
	if request.Method != deps.Management.Methods.Post {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	instanceID, matched := trafficRolloutInstanceID(deps, path)
	if !matched {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginNotFound, requestID)
		return
	}
	if deps.TrafficRollouts == nil || deps.Operations.Store == nil || deps.TrafficRollouts.MaximumConfigurationBytes < 1 ||
		deps.TrafficRollouts.SchemaVersion < 1 || deps.TrafficRolloutAPI.MaximumMetadataBytes < 1 || deps.TrafficRolloutAPI.EnvelopeOverheadBytes < 1 {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	metadata, configuration, ok := readTrafficRolloutMultipart(deps, response, request, requestID, deps.TrafficRollouts.MaximumConfigurationBytes)
	if !ok {
		return
	}
	defer clear(metadata)
	defer clear(configuration)
	if _, err := application.ParseTrafficRolloutPlan(metadata); err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginConfigInvalid, requestID)
		return
	}
	expectedRevision, valid := parseStrongRevisionETag(request.Header.Get(deps.Management.Headers.IfMatch))
	if !valid {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	idempotencyKey := request.Header.Get(deps.Management.Idempotency.Key)
	keyLength := utf8.RuneCountInString(idempotencyKey)
	if idempotencyKey == "" || keyLength < deps.Management.Idempotency.KeyMin || keyLength > deps.Management.Idempotency.KeyMax {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	operationID, err := newOperationID()
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	now := time.Now().UTC()
	digest := trafficRolloutRequestDigest(metadata, configuration)
	operation := models.Operation{
		ID: operationID, Kind: deps.TrafficRolloutAPI.OperationKind, State: deps.Management.Statuses.Pending,
		CreatedAt: now, RequestID: requestID, Actor: actor, Resource: instanceID,
	}
	reservation := models.OperationReservation{
		Operation: operation, Scope: path, Key: idempotencyKey, RequestDigest: digest,
	}
	result, created, err := deps.TrafficRollouts.Create(request.Context(), application.CreateTrafficRolloutCommand{
		ID: operationID, ExpectedRevision: expectedRevision, SchemaVersion: deps.TrafficRollouts.SchemaVersion,
		Configuration: configuration, Plan: metadata, Reservation: reservation,
		Audit: models.AuditRecord{
			Actor: actor, Action: deps.TrafficRolloutAPI.AuditAction, Resource: instanceID,
			Result: deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
		},
	})
	if err != nil {
		writeTrafficRolloutFailure(deps, response, requestID, err)
		return
	}
	if created {
		response.Header().Set(deps.Management.Headers.Location, deps.Management.Paths.Operations+deps.Management.Paths.PluginIDSeparator+result.ID)
	}
	deps.WriteJSON(response, http.StatusAccepted, map[string]any{
		deps.Management.JSON.OperationID: result.ID,
		deps.Management.JSON.State:       result.State,
		deps.Management.JSON.RequestID:   requestID,
	})
}

func TrafficRolloutApproveStage(deps PluginDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if request.Method != deps.Management.Methods.Post {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	instanceID, rolloutID, stageID, matched := trafficRolloutApprovalPath(deps, path)
	if !matched || deps.TrafficRollouts == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginNotFound, requestID)
		return
	}
	revision, valid := parseStrongRevisionETag(request.Header.Get(deps.Management.Headers.IfMatch))
	if !valid {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	key := request.Header.Get(deps.Management.Idempotency.Key)
	keyLength := utf8.RuneCountInString(key)
	if key == "" || keyLength < deps.Management.Idempotency.KeyMin || keyLength > deps.Management.Idempotency.KeyMax {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	operationID, err := newOperationID()
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	now := time.Now().UTC()
	digest := trafficRolloutRequestDigest([]byte(path), []byte(request.Header.Get(deps.Management.Headers.IfMatch)))
	operation := models.Operation{
		ID: operationID, Kind: deps.TrafficRolloutAPI.ApprovalOperationKind, State: deps.Management.Statuses.Completed,
		CreatedAt: now, RequestID: requestID, Actor: actor, Resource: rolloutID,
	}
	result, _, err := deps.TrafficRollouts.ApproveStage(request.Context(), models.OperationReservation{
		Operation: operation, Scope: path, Key: key, RequestDigest: digest,
	}, rolloutID, revision, stageID, models.AuditRecord{
		Actor: actor, Action: deps.TrafficRolloutAPI.ApprovalAuditAction, Resource: instanceID,
		Result: deps.AuditWords.Audit.Results.Succeeded, RequestID: requestID,
	})
	if err != nil {
		writeTrafficRolloutFailure(deps, response, requestID, err)
		return
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.OperationID: result.ID,
		deps.Management.JSON.State:       result.State,
		deps.Management.JSON.RequestID:   requestID,
	})
}

func readTrafficRolloutMultipart(
	deps PluginDependencies,
	response http.ResponseWriter,
	request *http.Request,
	requestID string,
	maximumConfigurationBytes int,
) ([]byte, []byte, bool) {
	mediaType, parameters, err := mime.ParseMediaType(request.Header.Get(deps.Management.Headers.ContentType))
	if err != nil || mediaType != deps.TrafficRolloutAPI.MultipartMediaType || parameters["boundary"] == "" {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return nil, nil, false
	}
	maximumBody := int64(maximumConfigurationBytes) + deps.TrafficRolloutAPI.MaximumMetadataBytes + deps.TrafficRolloutAPI.EnvelopeOverheadBytes
	request.Body = http.MaxBytesReader(response, request.Body, maximumBody)
	reader := multipart.NewReader(request.Body, parameters["boundary"])
	parts := make(map[string][]byte, 2)
	for len(parts) < 2 {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			clearMultipartParts(parts)
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return nil, nil, false
		}
		name := part.FormName()
		limit := int64(maximumConfigurationBytes)
		media := deps.TrafficRolloutAPI.ConfigurationMediaType
		if name == deps.TrafficRolloutAPI.MetadataPart {
			limit, media = deps.TrafficRolloutAPI.MaximumMetadataBytes, deps.TrafficRolloutAPI.MetadataMediaType
		} else if name != deps.TrafficRolloutAPI.ConfigurationPart {
			_ = part.Close()
			clearMultipartParts(parts)
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return nil, nil, false
		}
		if _, duplicate := parts[name]; duplicate {
			_ = part.Close()
			clearMultipartParts(parts)
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return nil, nil, false
		}
		partType, _, parseErr := mime.ParseMediaType(part.Header.Get(deps.Management.Headers.ContentType))
		if parseErr != nil || partType != media {
			_ = part.Close()
			clearMultipartParts(parts)
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return nil, nil, false
		}
		data, readErr := io.ReadAll(io.LimitReader(part, limit+1))
		_ = part.Close()
		if readErr != nil || int64(len(data)) > limit || !utf8.Valid(data) {
			clear(data)
			clearMultipartParts(parts)
			deps.WriteCatalogProblem(response, deps.Management.Codes.PluginConfigInvalid, requestID)
			return nil, nil, false
		}
		parts[name] = data
	}
	if len(parts) != 2 {
		clearMultipartParts(parts)
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return nil, nil, false
	}
	if _, nextErr := reader.NextPart(); !errors.Is(nextErr, io.EOF) {
		clearMultipartParts(parts)
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return nil, nil, false
	}
	return parts[deps.TrafficRolloutAPI.MetadataPart], parts[deps.TrafficRolloutAPI.ConfigurationPart], true
}

func trafficRolloutRequestDigest(metadata, configuration []byte) string {
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(metadata)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(metadata)
	binary.BigEndian.PutUint64(length[:], uint64(len(configuration)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(configuration)
	return hex.EncodeToString(hash.Sum(nil))
}

func clearMultipartParts(parts map[string][]byte) {
	for name, data := range parts {
		clear(data)
		delete(parts, name)
	}
}

func writeTrafficRolloutFailure(deps PluginDependencies, response http.ResponseWriter, requestID string, err error) {
	var conflict models.PluginConfigurationConflict
	var notFound models.PluginConfigurationNotFound
	var rolloutNotFound models.TrafficRolloutNotFound
	var invalid models.TrafficRolloutInvalid
	var rolloutConflict models.TrafficRolloutConflict
	var idempotency models.IdempotencyConflict
	var unavailable models.PluginConfigurationUnavailable
	switch {
	case errors.Is(err, application.ErrInvalidTrafficRolloutPlan), errors.As(err, &invalid):
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginConfigInvalid, requestID)
	case errors.As(err, &notFound), errors.As(err, &rolloutNotFound):
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginNotFound, requestID)
	case errors.As(err, &conflict), errors.As(err, &rolloutConflict):
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginRevisionConflict, requestID)
	case errors.As(err, &idempotency):
		deps.WriteCatalogProblem(response, deps.Management.Codes.IdempotencyConflict, requestID)
	case errors.As(err, &unavailable):
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	default:
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
	}
}
