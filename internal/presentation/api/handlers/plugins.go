package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
)

func PluginList(deps PluginDependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	deps.WritePage(response, deps.Plugins, request, requestID)
}

func IsPluginSettingsPath(deps PluginDependencies, path string) bool {
	separator := deps.Management.Paths.PluginIDSeparator
	suffix := deps.Management.Paths.PluginSettingsSuffix
	if separator == "" || suffix == "" || deps.Management.Paths.Plugins == "" {
		return false
	}
	prefix := deps.Management.Paths.Plugins + separator
	resource := strings.TrimPrefix(path, prefix)
	if resource == path || !strings.HasSuffix(resource, suffix) {
		return false
	}
	instanceID := strings.TrimSuffix(resource, suffix)
	return instanceID != "" && !strings.Contains(instanceID, separator)
}

func PluginSettings(deps PluginDependencies, response http.ResponseWriter, service *application.PluginConfigurationService, ctx context.Context, path, requestID string) {
	if service == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	prefix := deps.Management.Paths.Plugins + deps.Management.Paths.PluginIDSeparator
	instanceID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), deps.Management.Paths.PluginSettingsSuffix)
	revision, err := service.Current(ctx, instanceID)
	if err != nil {
		var missing models.PluginConfigurationNotFound
		if errors.As(err, &missing) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.PluginNotFound, requestID)
			return
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	response.Header().Set(deps.Management.Headers.ETag, revisionETag(revision.Revision))
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Revision: strconv.FormatInt(revision.Revision, 10),
		deps.Management.JSON.Digest:   revision.Digest,
		deps.Management.JSON.Config:   json.RawMessage(revision.SettingsJSON),
	})
}

func AdminSurfaceList(deps PluginDependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	if deps.ListAdminSurfaces == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginUnavailable, requestID)
		return
	}
	items, err := deps.ListAdminSurfaces(request.Context())
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginUnavailable, requestID)
		return
	}
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Items:     items,
		deps.Management.JSON.RequestID: requestID,
	})
}

func IsPluginDetailPath(deps PluginDependencies, path string) bool {
	separator := deps.Management.Paths.PluginIDSeparator
	if deps.Management.Paths.Plugins == "" || separator == "" {
		return false
	}
	prefix := deps.Management.Paths.Plugins + separator
	instanceID := strings.TrimPrefix(path, prefix)
	return instanceID != path && instanceID != "" && !strings.Contains(instanceID, separator)
}

func PluginDetail(deps PluginDependencies, response http.ResponseWriter, path, requestID string) {
	separator := deps.Management.Paths.PluginIDSeparator
	instanceID := strings.TrimPrefix(path, deps.Management.Paths.Plugins+separator)
	if deps.PluginIDField == "" {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}

	var matched map[string]any
	for _, item := range deps.Plugins {
		plugin, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if candidateID, ok := plugin[deps.PluginIDField].(string); ok && candidateID == instanceID {
			matched = plugin
			break
		}
	}
	if matched == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginNotFound, requestID)
		return
	}
	deps.WriteJSON(response, http.StatusOK, matched)
}

func revisionETag(revision int64) string {
	return strconv.Quote(strconv.FormatInt(revision, 10))
}

func PluginAdmin(deps PluginDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	instanceID, pageID, actionID, query, matched := parsePluginAdminPath(deps, path)
	if !matched {
		deps.WriteProblem(response, http.StatusNotFound, deps.Management.Codes.OperationNotFound, deps.Management.Diagnostics.OperationNotFound, requestID)
		return
	}
	outcome := false
	if deps.RecordAdminOutcome == nil {
		deps.WriteProblem(response, http.StatusServiceUnavailable, deps.AuditWords.Audit.StorageUnavailable.Code,
			deps.AuditWords.Audit.StorageUnavailable.Detail, requestID)
		return
	}
	buffered := newAdminResponseBuffer()
	destination := response
	defer func() {
		if err := deps.RecordAdminOutcome(request.Context(), actor, instanceID, requestID, outcome); err != nil {
			deps.WriteProblem(destination, http.StatusServiceUnavailable, deps.AuditWords.Audit.StorageUnavailable.Code,
				deps.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		buffered.Flush(destination)
	}()
	response = buffered
	if deps.AdminSurfaceDigest == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginUnavailable, requestID)
		return
	}
	if request.Method != deps.Management.Methods.Post {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	mediaType, params, mediaErr := mime.ParseMediaType(request.Header.Get(deps.Management.Headers.ContentType))
	artifactAction := mediaErr == nil && mediaType == deps.AdminLimits.MultipartMediaType
	requestedSurfaceDigest := strings.TrimSpace(request.Header.Get(deps.Management.Headers.AdminSurfaceDigest))
	if requestedSurfaceDigest == "" {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	if !query && !artifactAction && strings.TrimSpace(request.Header.Get(deps.Management.Headers.IfMatch)) == "" {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	idempotencyKey := request.Header.Get(deps.Management.Idempotency.Key)
	if !query {
		length := utf8.RuneCountInString(idempotencyKey)
		if length < deps.Management.Idempotency.KeyMin || length > deps.Management.Idempotency.KeyMax {
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return
		}
	}
	surfaceDigest, err := deps.AdminSurfaceDigest(request.Context(), instanceID)
	if err != nil || surfaceDigest == "" {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginUnavailable, requestID)
		return
	}
	if requestedSurfaceDigest != surfaceDigest {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginRevisionConflict, requestID)
		return
	}
	invocation := AdminInvocation{
		CallerID: actor, InstanceID: instanceID, PageID: pageID, ActionID: actionID,
		SurfaceDigest: surfaceDigest, RequestID: requestID, IdempotencyKey: idempotencyKey,
		IfMatch: request.Header.Get(deps.Management.Headers.IfMatch),
	}
	var result PluginAdminResult
	if mediaErr == nil && mediaType == deps.AdminLimits.MultipartMediaType {
		if deps.ForwardArtifact == nil {
			deps.WriteCatalogProblem(response, deps.Management.Codes.PluginUnavailable, requestID)
			return
		}
		result, err = forwardPluginArtifact(deps, response, request, invocation, params["boundary"])
	} else if mediaErr == nil && mediaType == deps.Management.ContentTypes.JSON {
		if deps.DispatchAdmin == nil {
			deps.WriteCatalogProblem(response, deps.Management.Codes.PluginUnavailable, requestID)
			return
		}
		if deps.AdminLimits.JSONRequestBytes <= 0 {
			deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
			return
		}
		input, decodeErr := decodePluginSettings(request.Body, int(deps.AdminLimits.JSONRequestBytes))
		if decodeErr != nil {
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return
		}
		result, err = deps.DispatchAdmin(request.Context(), invocation, input)
	} else {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	if err != nil {
		if errors.Is(err, errAdminArtifactTooLarge) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.ArtifactTooLarge, requestID)
			return
		}
		if errors.Is(err, errAdminArtifactInvalid) {
			deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
			return
		}
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginUnavailable, requestID)
		return
	}
	if result.Status < http.StatusOK || result.Status > 599 || !json.Valid(result.Body) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.PluginUnavailable, requestID)
		return
	}
	if result.ContentType == "" {
		result.ContentType = deps.Management.ContentTypes.JSON
	}
	response.Header().Set(deps.Management.Headers.ContentType, result.ContentType)
	response.WriteHeader(result.Status)
	_, _ = response.Write(result.Body)
	outcome = result.Status < http.StatusBadRequest
}

func parsePluginAdminPath(deps PluginDependencies, path string) (instanceID, pageID, actionID string, query, matched bool) {
	root := deps.Management.Paths.Plugins + deps.Management.Paths.PluginIDSeparator
	resource := strings.TrimPrefix(path, root)
	if resource == path {
		return "", "", "", false, false
	}
	instance, remaining, found := strings.Cut(resource, deps.Management.Paths.PluginIDSeparator)
	pagePrefix := deps.Management.Paths.AdminPages + deps.Management.Paths.PluginIDSeparator
	if !found || instance == "" || !strings.HasPrefix(remaining, pagePrefix) {
		return "", "", "", false, false
	}
	parts := strings.Split(strings.TrimPrefix(remaining, pagePrefix), deps.Management.Paths.PluginIDSeparator)
	if len(parts) == 2 && parts[0] != "" && parts[1] == deps.Management.Paths.AdminQueryAction {
		return instance, parts[0], deps.Management.Paths.AdminQueryAction, true, true
	}
	if len(parts) == 3 && parts[0] != "" && parts[1] == deps.Management.Paths.AdminActions && parts[2] != "" {
		return instance, parts[0], parts[2], false, true
	}
	return "", "", "", false, false
}

func forwardPluginArtifact(deps PluginDependencies, response http.ResponseWriter, request *http.Request, invocation AdminInvocation, boundary string) (PluginAdminResult, error) {
	limits := deps.AdminLimits
	if deps.ForwardArtifact == nil || limits.ArtifactBytes <= 0 || limits.MinimumArtifact < 0 || limits.MetadataBytes <= 0 || limits.MaximumRequest <= 0 ||
		limits.MetadataPartName == "" || limits.ArtifactPartName == "" || boundary == "" {
		return PluginAdminResult{}, errAdminArtifactInvalid
	}
	request.Body = http.MaxBytesReader(response, request.Body, limits.MaximumRequest)
	multipartReader := multipart.NewReader(request.Body, boundary)
	metadataPart, err := multipartReader.NextPart()
	if err != nil {
		return PluginAdminResult{}, classifyAdminMultipartReadError(err)
	}
	if metadataPart.FormName() != limits.MetadataPartName {
		return PluginAdminResult{}, errAdminArtifactInvalid
	}
	metadataType, _, err := mime.ParseMediaType(metadataPart.Header.Get(deps.Management.Headers.ContentType))
	if err != nil || metadataType != limits.MetadataMediaType {
		_ = metadataPart.Close()
		return PluginAdminResult{}, errAdminArtifactInvalid
	}
	metadata, err := io.ReadAll(io.LimitReader(metadataPart, limits.MetadataBytes+1))
	closeErr := metadataPart.Close()
	if err == nil {
		err = closeErr
	}
	if int64(len(metadata)) > limits.MetadataBytes {
		return PluginAdminResult{}, errAdminArtifactTooLarge
	}
	if err != nil || len(metadata) == 0 || !json.Valid(metadata) {
		return PluginAdminResult{}, errAdminArtifactInvalid
	}
	if _, err := decodePluginSettings(bytes.NewReader(metadata), int(limits.MetadataBytes)); err != nil {
		return PluginAdminResult{}, errAdminArtifactInvalid
	}
	artifactPart, err := multipartReader.NextPart()
	if err != nil {
		return PluginAdminResult{}, classifyAdminMultipartReadError(err)
	}
	if artifactPart.FormName() != limits.ArtifactPartName {
		if artifactPart != nil {
			_ = artifactPart.Close()
		}
		return PluginAdminResult{}, errAdminArtifactInvalid
	}
	contentType, _, err := mime.ParseMediaType(artifactPart.Header.Get(deps.Management.Headers.ContentType))
	if err != nil {
		_ = artifactPart.Close()
		return PluginAdminResult{}, errAdminArtifactInvalid
	}
	checked := &checkedAdminArtifact{part: artifactPart, reader: multipartReader, maximum: limits.ArtifactBytes}
	result, dispatchErr := deps.ForwardArtifact(request.Context(), invocation, metadata, contentType, checked)
	if checked.tooLarge {
		return PluginAdminResult{}, errAdminArtifactTooLarge
	}
	if checked.invalid {
		return PluginAdminResult{}, errAdminArtifactInvalid
	}
	if dispatchErr != nil {
		return PluginAdminResult{}, dispatchErr
	}
	if checked.read < limits.MinimumArtifact {
		return PluginAdminResult{}, errAdminArtifactInvalid
	}
	return result, dispatchErr
}

type checkedAdminArtifact struct {
	part     *multipart.Part
	reader   *multipart.Reader
	maximum  int64
	read     int64
	checked  bool
	tooLarge bool
	invalid  bool
}

func (artifact *checkedAdminArtifact) Read(buffer []byte) (int, error) {
	if artifact.read > artifact.maximum {
		artifact.tooLarge = true
		return 0, errAdminArtifactTooLarge
	}
	remaining := artifact.maximum + 1 - artifact.read
	if int64(len(buffer)) > remaining {
		buffer = buffer[:remaining]
	}
	n, err := artifact.part.Read(buffer)
	artifact.read += int64(n)
	var maximumError *http.MaxBytesError
	if errors.As(err, &maximumError) {
		artifact.tooLarge = true
		return n, errAdminArtifactTooLarge
	}
	if artifact.read > artifact.maximum {
		artifact.tooLarge = true
		return n, errAdminArtifactTooLarge
	}
	if err == io.EOF && !artifact.checked {
		artifact.checked = true
		extra, nextErr := artifact.reader.NextPart()
		if nextErr != io.EOF {
			var maximumError *http.MaxBytesError
			if errors.As(nextErr, &maximumError) {
				artifact.tooLarge = true
				return n, errAdminArtifactTooLarge
			}
			artifact.invalid = true
			if extra != nil {
				_ = extra.Close()
			}
			return n, errAdminArtifactInvalid
		}
	}
	return n, err
}

func classifyAdminMultipartReadError(err error) error {
	var maximumError *http.MaxBytesError
	if errors.As(err, &maximumError) {
		return errAdminArtifactTooLarge
	}
	return errAdminArtifactInvalid
}

func (artifact *checkedAdminArtifact) Close() error { return artifact.part.Close() }

type adminResponseBuffer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newAdminResponseBuffer() *adminResponseBuffer {
	return &adminResponseBuffer{header: make(http.Header)}
}

func (buffer *adminResponseBuffer) Header() http.Header { return buffer.header }

func (buffer *adminResponseBuffer) WriteHeader(status int) {
	if buffer.status == 0 {
		buffer.status = status
	}
}

func (buffer *adminResponseBuffer) Write(body []byte) (int, error) {
	if buffer.status == 0 {
		buffer.status = http.StatusOK
	}
	return buffer.body.Write(body)
}

func (buffer *adminResponseBuffer) Flush(destination http.ResponseWriter) {
	for name, values := range buffer.header {
		destination.Header()[name] = append([]string(nil), values...)
	}
	status := buffer.status
	if status == 0 {
		status = http.StatusOK
	}
	destination.WriteHeader(status)
	_, _ = destination.Write(buffer.body.Bytes())
}

var (
	errAdminArtifactInvalid  = errors.New("invalid plugin artifact request")
	errAdminArtifactTooLarge = errors.New("plugin artifact request too large")
)
