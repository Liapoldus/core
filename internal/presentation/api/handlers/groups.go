package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
)

type groupReleaseMetadata struct {
	IdempotencyKey          string
	ExpectedCurrentRevision *string
}

func GroupList(deps GroupDependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	if deps.GroupService.Store == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	groups, err := deps.GroupService.List(request.Context())
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(groups.Items))
	for _, group := range groups.Items {
		items = append(items, groupResponse(group, deps.Management))
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Items:     items,
		deps.Management.JSON.RequestID: requestID,
	})
}

func GroupCreate(deps GroupDependencies, response http.ResponseWriter, request *http.Request, requestID, actor string) {
	fail := func(code string) {
		if err := deps.RecordAudit(request.Context(), actor, deps.AuditWords.Audit.Actions.GroupCreate, deps.AuditWords.Audit.Resources.Groups, deps.AuditWords.Audit.Results.Failed, requestID, "", ""); err != nil {
			deps.WriteProblem(response, http.StatusServiceUnavailable, deps.AuditWords.Audit.StorageUnavailable.Code, deps.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		deps.WriteCatalogProblem(response, code, requestID)
	}
	decoder := json.NewDecoder(request.Body)
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		fail(deps.Management.Codes.InvalidRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		fail(deps.Management.Codes.InvalidRequest)
		return
	}
	if len(fields) != 2 {
		fail(deps.Management.Codes.InvalidRequest)
		return
	}
	for key := range fields {
		if key != deps.Management.JSON.ID && key != deps.Management.JSON.IdempotencyKey {
			fail(deps.Management.Codes.InvalidRequest)
			return
		}
	}
	var id, key string
	if json.Unmarshal(fields[deps.Management.JSON.ID], &id) != nil || json.Unmarshal(fields[deps.Management.JSON.IdempotencyKey], &key) != nil || len(key) < deps.Management.Idempotency.KeyMin || len(key) > deps.Management.Idempotency.KeyChars || !ascii(key) {
		fail(deps.Management.Codes.InvalidRequest)
		return
	}
	validID, err := regexp.MatchString(deps.Management.Paths.GroupIDPattern, id)
	if err != nil {
		fail(deps.Management.Codes.ManagementUnavailable)
		return
	}
	if !validID {
		fail(deps.Management.Codes.InvalidRequest)
		return
	}
	if deps.GroupService.Store == nil {
		fail(deps.Management.Codes.ManagementUnavailable)
		return
	}
	auditRecord := models.AuditRecord{
		Actor:     actor,
		Action:    deps.AuditWords.Audit.Actions.GroupCreate,
		Resource:  deps.AuditWords.Audit.Resources.Groups,
		Result:    deps.AuditWords.Audit.Results.Succeeded,
		RequestID: requestID,
	}
	group, err := deps.GroupService.Create(request.Context(), id, auditRecord)
	if err != nil {
		var auditFailure models.AuditAppendError
		if errors.As(err, &auditFailure) {
			deps.WriteProblem(response, http.StatusServiceUnavailable, deps.AuditWords.Audit.StorageUnavailable.Code, deps.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		var exists models.GroupAlreadyExists
		if errors.As(err, &exists) {
			fail(deps.Management.Codes.GroupAlreadyExists)
			return
		}
		fail(deps.Management.Codes.ManagementUnavailable)
		return
	}
	response.Header().Set(deps.Management.Headers.Location, deps.Management.Paths.GroupByID+id)
	result := groupResponse(group, deps.Management)
	result[deps.Management.JSON.RequestID] = requestID
	deps.WriteJSON(response, http.StatusCreated, result)
}

func GroupGet(deps GroupDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) {
	if deps.GroupService.Store == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	id := strings.TrimPrefix(path, deps.Management.Paths.GroupByID)
	if id == "" || strings.Contains(id, deps.Management.Paths.GroupIDSeparator) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
		return
	}
	group, err := deps.GroupService.Get(request.Context(), id)
	if err != nil {
		var notFound models.GroupNotFound
		if errors.As(err, &notFound) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
			return
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	result := groupResponse(group, deps.Management)
	result[deps.Management.JSON.RequestID] = requestID
	deps.WriteJSON(response, http.StatusOK, result)
}

func GroupReleases(deps GroupDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) {
	if deps.GroupService.Store == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	suffix := deps.Management.Paths.GroupIDSeparator + deps.Management.Paths.GroupReleases
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, deps.Management.Paths.GroupByID), suffix)
	if groupID == "" || strings.Contains(groupID, deps.Management.Paths.GroupIDSeparator) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
		return
	}
	limit := 0
	if rawLimit := request.URL.Query().Get(deps.Management.JSON.Limit); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return
		}
		limit = parsed
	}
	page, err := deps.GroupService.ListRevisions(request.Context(), groupID, request.URL.Query().Get(deps.Management.JSON.Cursor), limit)
	if err != nil {
		var notFound models.GroupNotFound
		if errors.As(err, &notFound) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
			return
		}
		var invalidPage models.GroupRevisionPageError
		if errors.As(err, &invalidPage) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, revision := range page.Items {
		items = append(items, map[string]any{
			deps.Management.JSON.ID:              revision.ID,
			deps.Management.JSON.GroupID:         revision.GroupID,
			deps.Management.JSON.CaddyfileDigest: revision.CaddyfileDigest,
			deps.Management.JSON.ArtifactDigest:  revision.ArtifactDigest,
			deps.Management.JSON.CreatedAt:       revision.CreatedAt,
			deps.Management.JSON.Actor:           revision.Actor,
		})
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Items:      items,
		deps.Management.JSON.NextCursor: page.NextCursor,
		deps.Management.JSON.RequestID:  requestID,
	})
}

func GroupRelease(deps GroupDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) {
	if deps.GroupService.Store == nil || deps.GroupService.ContentReader == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	suffix := deps.Management.Paths.GroupIDSeparator + deps.Management.Paths.GroupReleases + deps.Management.Paths.GroupIDSeparator
	resource := strings.TrimPrefix(path, deps.Management.Paths.GroupByID)
	groupID, revisionID, found := strings.Cut(resource, suffix)
	if !found || groupID == "" || revisionID == "" || strings.Contains(revisionID, deps.Management.Paths.GroupIDSeparator) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
		return
	}
	detail, err := deps.GroupService.GetRevisionDetail(request.Context(), groupID, revisionID)
	if err != nil {
		var notFound models.GroupRevisionNotFound
		if errors.As(err, &notFound) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
			return
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	revision := detail.Revision
	frontends := make([]map[string]any, 0, len(detail.Frontends))
	for _, frontend := range detail.Frontends {
		frontends = append(frontends, map[string]any{
			deps.Management.JSON.ID:     frontend.ID,
			deps.Management.JSON.Digest: frontend.Digest,
			deps.Management.JSON.Files:  frontend.Files,
		})
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.ID:              revision.ID,
		deps.Management.JSON.GroupID:         revision.GroupID,
		deps.Management.JSON.Caddyfile:       detail.Caddyfile,
		deps.Management.JSON.CaddyfileDigest: revision.CaddyfileDigest,
		deps.Management.JSON.ArtifactDigest:  revision.ArtifactDigest,
		deps.Management.JSON.Frontends:       frontends,
		deps.Management.JSON.CreatedAt:       revision.CreatedAt,
		deps.Management.JSON.Actor:           revision.Actor,
		deps.Management.JSON.RequestID:       requestID,
	})
}

func GroupPublish(deps GroupDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.GroupReleases == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	groupID, metadata, caddyfile, artifact, ok := readGroupPublishRequest(deps, response, request, path, requestID)
	if !ok {
		return
	}
	operation, ok := acceptGroupPublish(deps, response, request, requestID, actor, groupID, metadata, caddyfile, artifact)
	if !ok {
		return
	}
	writeGroupOperationAccepted(deps, response, operation, requestID)
}

func readGroupPublishRequest(deps GroupDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) (string, groupReleaseMetadata, []byte, []byte, bool) {
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, deps.Management.Paths.GroupByID), deps.Management.Paths.GroupIDSeparator+deps.Management.Paths.GroupReleases)
	if groupID == "" || strings.Contains(groupID, deps.Management.Paths.GroupIDSeparator) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
		return "", groupReleaseMetadata{}, nil, nil, false
	}
	contentType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != deps.GroupReleasePolicy.MultipartContentType || parameters["boundary"] == "" {
		deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", groupReleaseMetadata{}, nil, nil, false
	}
	request.Body = http.MaxBytesReader(response, request.Body, deps.GroupReleasePolicy.RequestLimitBytes)
	metadata, caddyfile, artifact, err := readGroupReleaseMultipart(multipart.NewReader(request.Body, parameters["boundary"]), deps.GroupReleasePolicy, deps.Management)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
			return "", groupReleaseMetadata{}, nil, nil, false
		}
		var archiveError models.GroupReleaseArchiveError
		if errors.As(err, &archiveError) {
			if archiveError.TooLarge {
				deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
				return "", groupReleaseMetadata{}, nil, nil, false
			}
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.ArtifactInvalidCode, requestID)
			return "", groupReleaseMetadata{}, nil, nil, false
		}
		deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", groupReleaseMetadata{}, nil, nil, false
	}
	return groupID, metadata, caddyfile, artifact, true
}

func acceptGroupPublish(deps GroupDependencies, response http.ResponseWriter, request *http.Request, requestID, actor, groupID string, metadata groupReleaseMetadata, caddyfile, artifact []byte) (models.Operation, bool) {
	operation, _, err := deps.GroupReleases.Accept(request.Context(), models.GroupReleaseCommand{
		GroupID: groupID, Actor: actor, RequestID: requestID,
		IdempotencyKey:          metadata.IdempotencyKey,
		IdempotencyScope:        deps.GroupReleasePolicy.ScopePrefix + groupID + deps.GroupReleasePolicy.ScopeSuffix,
		ExpectedCurrentRevision: metadata.ExpectedCurrentRevision,
		IdempotencyWindow:       deps.GroupReleasePolicy.IdempotencyWindow,
		Caddyfile:               caddyfile, Artifact: artifact,
	})
	if err != nil {
		var driftBlocked models.GroupDriftBlocked
		if errors.As(err, &driftBlocked) {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.DriftBlockedCode, requestID)
			return models.Operation{}, false
		}
		var conflict models.GroupRevisionConflict
		if errors.As(err, &conflict) {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.RevisionConflictCode, requestID)
			return models.Operation{}, false
		}
		var idempotencyConflict models.IdempotencyConflict
		if errors.As(err, &idempotencyConflict) {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.IdempotencyConflictCode, requestID)
			return models.Operation{}, false
		}
		var groupNotFound models.GroupNotFound
		if errors.As(err, &groupNotFound) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
			return models.Operation{}, false
		}
		var validation models.GroupReleaseValidationError
		if errors.As(err, &validation) {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.CaddyAdaptFailedCode, requestID)
			return models.Operation{}, false
		}
		var archiveError models.GroupReleaseArchiveError
		if errors.As(err, &archiveError) {
			if archiveError.TooLarge {
				deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
				return models.Operation{}, false
			}
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.ArtifactInvalidCode, requestID)
			return models.Operation{}, false
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return models.Operation{}, false
	}
	return operation, true
}

func writeGroupOperationAccepted(deps GroupDependencies, response http.ResponseWriter, operation models.Operation, requestID string) {
	state := operation.State
	if state != deps.GroupReleasePolicy.PendingState && state != deps.GroupReleasePolicy.RunningState {
		state = deps.GroupReleasePolicy.PendingState
	}
	deps.WriteJSON(response, http.StatusAccepted, map[string]any{
		deps.Management.JSON.OperationID: operation.ID,
		deps.Management.JSON.State:       state,
		deps.Management.JSON.RequestID:   requestID,
	})
}

func GroupRollback(deps GroupDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.GroupReleases == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	groupID, idempotencyKey, expected, ok := readGroupRollbackRequest(deps, response, request, path, requestID)
	if !ok {
		return
	}
	operation, ok := acceptGroupRollback(deps, response, request, requestID, actor, groupID, idempotencyKey, expected)
	if !ok {
		return
	}
	writeGroupOperationAccepted(deps, response, operation, requestID)
}

func readGroupRollbackRequest(deps GroupDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) (string, string, *string, bool) {
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, deps.Management.Paths.GroupByID), deps.Management.Paths.GroupIDSeparator+deps.Management.Paths.GroupRollback)
	if groupID == "" || strings.Contains(groupID, deps.Management.Paths.GroupIDSeparator) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
		return "", "", nil, false
	}
	contentType, _, contentTypeErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if contentTypeErr != nil || contentType != deps.GroupReleasePolicy.MetadataContentType {
		deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", "", nil, false
	}
	request.Body = http.MaxBytesReader(response, request.Body, deps.GroupReleasePolicy.RequestLimitBytes)
	fields := make(map[string]json.RawMessage)
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&fields); err != nil || fields == nil || len(fields) != 2 {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
			return "", "", nil, false
		}
		deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", "", nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", "", nil, false
	}
	keyJSON, hasKey := fields[deps.Management.JSON.IdempotencyKey]
	expectedJSON, hasExpected := fields[deps.GroupReleasePolicy.ExpectedCurrentRevisionField]
	var idempotencyKey string
	var expected *string
	if !hasKey || !hasExpected || json.Unmarshal(keyJSON, &idempotencyKey) != nil || len(idempotencyKey) < deps.Management.Idempotency.KeyMin || len(idempotencyKey) > deps.Management.Idempotency.KeyChars || !ascii(idempotencyKey) || json.Unmarshal(expectedJSON, &expected) != nil {
		deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", "", nil, false
	}
	if expected != nil {
		valid, err := regexp.MatchString(deps.GroupReleasePolicy.RevisionIDPattern, *expected)
		if err != nil || !valid {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.InvalidRequestCode, requestID)
			return "", "", nil, false
		}
	}
	return groupID, idempotencyKey, expected, true
}

func acceptGroupRollback(deps GroupDependencies, response http.ResponseWriter, request *http.Request, requestID, actor, groupID, idempotencyKey string, expected *string) (models.Operation, bool) {
	operation, _, err := deps.GroupReleases.Rollback(request.Context(), models.GroupRollbackCommand{
		GroupID: groupID, Actor: actor, RequestID: requestID,
		IdempotencyKey:          idempotencyKey,
		IdempotencyScope:        deps.GroupReleasePolicy.ScopePrefix + groupID + deps.GroupReleasePolicy.RollbackScopeSuffix,
		ExpectedCurrentRevision: expected, IdempotencyWindow: deps.GroupReleasePolicy.IdempotencyWindow,
	})
	if err != nil {
		var conflict models.GroupRevisionConflict
		if errors.As(err, &conflict) {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.RevisionConflictCode, requestID)
			return models.Operation{}, false
		}
		var idempotencyConflict models.IdempotencyConflict
		if errors.As(err, &idempotencyConflict) {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.IdempotencyConflictCode, requestID)
			return models.Operation{}, false
		}
		var groupNotFound models.GroupNotFound
		if errors.As(err, &groupNotFound) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
			return models.Operation{}, false
		}
		var revisionNotFound models.GroupRevisionNotFound
		if errors.As(err, &revisionNotFound) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.GroupNotFound, requestID)
			return models.Operation{}, false
		}
		var validation models.GroupReleaseValidationError
		if errors.As(err, &validation) {
			deps.WriteCatalogProblem(response, deps.GroupReleasePolicy.CaddyAdaptFailedCode, requestID)
			return models.Operation{}, false
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return models.Operation{}, false
	}
	return operation, true
}

func readGroupReleaseMultipart(reader *multipart.Reader, policy models.GroupReleasePolicy, management config.ManagementWords) (groupReleaseMetadata, []byte, []byte, error) {
	var result groupReleaseMetadata
	var metadataBytes, caddyfile, artifact []byte
	metadataSeen, caddyfileSeen := false, false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, nil, nil, err
		}
		contents, err := io.ReadAll(io.LimitReader(part, policy.RequestLimitBytes+1))
		_ = part.Close()
		if err != nil || int64(len(contents)) > policy.RequestLimitBytes {
			return result, nil, nil, errors.New(policy.InvalidConfiguration)
		}
		switch part.FormName() {
		case policy.MetadataPart:
			if metadataSeen {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			partType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if typeErr != nil || partType != policy.MetadataContentType {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			metadataSeen = true
			metadataBytes = contents
		case policy.CaddyfilePart:
			if caddyfileSeen || !utf8.Valid(contents) {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			partType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if typeErr != nil || partType != policy.CaddyfileContentType {
				return result, nil, nil, errors.New(policy.InvalidConfiguration)
			}
			caddyfileSeen = true
			caddyfile = contents
		case policy.ArtifactPart:
			if artifact != nil {
				return result, nil, nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration)}
			}
			partType, _, typeErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
			fileName := part.FileName()
			if typeErr != nil || partType != policy.ArtifactContentType || !strings.HasSuffix(strings.ToLower(fileName), policy.ArtifactSuffix) || strings.ContainsAny(fileName, "/\\") {
				return result, nil, nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration)}
			}
			if int64(len(contents)) > policy.CompressedArtifactLimitBytes {
				return result, nil, nil, models.GroupReleaseArchiveError{Cause: errors.New(policy.InvalidConfiguration), TooLarge: true}
			}
			artifact = contents
		default:
			return result, nil, nil, errors.New(policy.InvalidConfiguration)
		}
	}
	if !metadataSeen || !caddyfileSeen || len(caddyfile) == 0 {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal(metadataBytes, &fields); err != nil || len(fields) != 2 {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	keyJSON, hasKey := fields[policy.MetadataIdempotencyKeyField]
	expectedJSON, hasExpected := fields[policy.MetadataExpectedRevisionField]
	if !hasKey || !hasExpected || json.Unmarshal(keyJSON, &result.IdempotencyKey) != nil || len(result.IdempotencyKey) < management.Idempotency.KeyMin || len(result.IdempotencyKey) > management.Idempotency.KeyChars || !ascii(result.IdempotencyKey) {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	var expected *string
	if err := json.Unmarshal(expectedJSON, &expected); err != nil {
		return result, nil, nil, errors.New(policy.InvalidConfiguration)
	}
	if expected != nil {
		valid, err := regexp.MatchString(policy.RevisionIDPattern, *expected)
		if err != nil || !valid {
			return result, nil, nil, errors.New(policy.InvalidConfiguration)
		}
		result.ExpectedCurrentRevision = expected
	}
	return result, caddyfile, artifact, nil
}

func groupResponse(group models.Group, management config.ManagementWords) map[string]any {
	state := management.Statuses.Ready
	if group.CurrentRevisionID == nil {
		state = management.Statuses.Empty
	}
	return map[string]any{
		management.JSON.ID:               group.ID,
		management.JSON.Kind:             group.Kind,
		management.JSON.Active:           group.Active,
		management.JSON.CurrentRevision:  group.CurrentRevisionID,
		management.JSON.PreviousRevision: group.PreviousRevisionID,
		management.JSON.State:            state,
	}
}

func ascii(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}
