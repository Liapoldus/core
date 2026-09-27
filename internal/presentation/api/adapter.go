// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"golang.org/x/crypto/bcrypt"
)

func (server *Server) handleHealthz(response http.ResponseWriter, request *http.Request, requestID string) bool {
	if request.URL.Path != server.Management.Paths.Healthz {
		return false
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		server.writeProblem(response, http.StatusMethodNotAllowed, "method_not_allowed", "health endpoint accepts GET and HEAD", requestID)
		return true
	}
	server.writeJSON(response, http.StatusOK, map[string]any{server.Management.JSON.Status: server.Management.Statuses.OK, server.Management.JSON.RequestID: requestID})
	return true
}

func (server *Server) authorizeManagementRequest(response http.ResponseWriter, request *http.Request, requestID string) (string, bool) {
	if server.RequireClientCertificate && (request.TLS == nil || len(request.TLS.PeerCertificates) == 0) {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementMTLSRequired, requestID)
		return "", false
	}
	actor, authorized, err := server.authenticate(request.Context(), request.Header.Get("Authorization"))
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return "", false
	}
	if !authorized {
		server.writeCatalogProblem(response, server.Management.Codes.BearerRequired, requestID)
		return "", false
	}
	return actor, true
}

func (server *Server) handleReadiness(response http.ResponseWriter, request *http.Request, requestID string) {
	server.mu.RLock()
	state, reason := server.DataPlaneState, server.DataPlaneReason
	provider := server.DataPlaneReadiness
	server.mu.RUnlock()
	if provider != nil {
		state, reason = provider(request.Context())
	}
	readiness := map[string]any{server.Management.JSON.State: state}
	if state == server.Management.Statuses.NotReady {
		readiness[server.Management.JSON.Reason] = reason
	}
	server.writeJSON(response, 200, map[string]any{
		server.Management.JSON.Caddy: map[string]any{
			server.Management.JSON.Variant: server.CaddyVariant,
			server.Management.JSON.BuildID: server.CaddyBuildID,
			server.Management.JSON.Modules: server.CaddyModules,
		},
		server.Management.JSON.Drift:              false,
		server.Management.JSON.DataPlaneReadiness: readiness,
		server.Management.JSON.RequestID:          requestID,
	})
}

func (server *Server) handleAuditList(response http.ResponseWriter, request *http.Request, requestID string) {
	if server.Audit == nil {
		server.writeJSON(response, http.StatusOK, map[string]any{server.Management.JSON.Items: []models.AuditRecord{}, server.Management.JSON.NextCursor: nil, server.Management.JSON.RequestID: requestID})
		return
	}
	limit := server.Management.Pagination.LimitDefault
	if rawLimit := request.URL.Query().Get(server.Management.JSON.Limit); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
			return
		}
		limit = parsed
	}
	page, err := server.Audit.Records(request.Context(), request.URL.Query().Get(server.Management.JSON.Cursor), limit)
	if err != nil {
		var pageError models.AuditPageError
		if errors.As(err, &pageError) {
			server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
			return
		}
		server.writeProblem(response, http.StatusServiceUnavailable, server.AuditWords.Audit.StorageUnavailable.Code, server.AuditWords.Audit.StorageUnavailable.Detail, requestID)
		return
	}
	server.writeJSON(response, http.StatusOK, map[string]any{server.Management.JSON.Items: page.Items, server.Management.JSON.NextCursor: page.NextCursor, server.Management.JSON.RequestID: requestID})
}

func (server *Server) handlePluginRestart(response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if server.RestartPlugin == nil {
		server.writeProblem(response, 501, "not_implemented", "plugin restart is unavailable", requestID)
		return
	}
	instance := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.Plugins+"/"), "/"+server.Management.Paths.Restart)
	if len(instance) == 0 || strings.Contains(instance, "/") {
		server.writeProblem(response, http.StatusNotFound, "not_found", "plugin resource not found", requestID)
		return
	}
	if server.Operations.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	op, err := server.RestartPlugin(request.Context(), instance)
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	if op.ID == "" || op.Kind == "" || (op.State != server.Management.Statuses.Pending && op.State != server.Management.Statuses.Running) {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	op.RequestID = requestID
	op.Actor = actor
	op.Resource = instance
	if op.CreatedAt.IsZero() {
		op.CreatedAt = time.Now().UTC()
	}
	if err := server.Operations.Create(request.Context(), op); err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	server.writeJSON(response, http.StatusAccepted, map[string]any{
		server.Management.JSON.OperationID: op.ID,
		server.Management.JSON.State:       op.State,
		server.Management.JSON.RequestID:   requestID,
	})
}

func (server *Server) handleOperationGet(response http.ResponseWriter, request *http.Request, path, requestID string) {
	id := strings.TrimPrefix(path, server.Management.Paths.Operations+"/")
	if server.Operations.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	operation, err := server.Operations.Get(request.Context(), id)
	if err != nil {
		var notFound models.OperationNotFound
		if errors.As(err, &notFound) {
			server.writeProblem(response, http.StatusNotFound, server.Management.Codes.OperationNotFound, server.Management.Diagnostics.OperationNotFound, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	result := map[string]any{
		server.Management.JSON.ID:        operation.ID,
		server.Management.JSON.Kind:      operation.Kind,
		server.Management.JSON.State:     operation.State,
		server.Management.JSON.CreatedAt: operation.CreatedAt,
		server.Management.JSON.RequestID: operation.RequestID,
	}
	if operation.UpdatedAt != nil {
		result[server.Management.JSON.UpdatedAt] = *operation.UpdatedAt
	}
	server.writeJSON(response, http.StatusOK, result)
}

func (server *Server) handleCaddyAdmin(response http.ResponseWriter, request *http.Request, requestID, actor string) {
	if server.AdminMutations == nil || server.AdminWords.Paths.ManagementPrefix == "" {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	allowed := containsString(server.AdminWords.Methods.ReadOnly, request.Method) || containsString(server.AdminWords.Methods.Mutating, request.Method)
	if !allowed {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	path := request.URL.Path
	if !strings.HasPrefix(path, server.AdminWords.Paths.ManagementPrefix) {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	suffix := strings.TrimPrefix(path, server.AdminWords.Paths.ManagementPrefix)
	if suffix == "" || containsAnyString(suffix, server.AdminWords.Paths.ForbiddenPathCharacters) || invalidCaddyPathSegment(suffix, server.AdminWords.Paths.PathSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	adminPath := server.AdminWords.Paths.LeadingSlash + suffix
	if request.URL.RawQuery != "" {
		adminPath += server.AdminWords.Paths.QuerySeparator + request.URL.RawQuery
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, server.AdminWords.Limits.RequestBodyBytes+1))
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	if int64(len(body)) > server.AdminWords.Limits.RequestBodyBytes {
		server.writeCatalogProblem(response, server.Management.Codes.ArtifactTooLarge, requestID)
		return
	}
	headers := make(map[string][]string)
	for _, name := range server.AdminWords.Headers.ForwardRequest {
		if values := request.Header.Values(name); len(values) > 0 {
			headers[name] = append([]string(nil), values...)
		}
	}
	result, err := server.AdminMutations.Handle(request.Context(), models.AdminMutationCommand{
		Request: models.CaddyAdminRequest{
			Method: request.Method, Path: adminPath, Headers: headers, Body: body,
		},
		Actor: actor, RequestID: requestID,
	})
	if err != nil || result.Status < http.StatusContinue || result.Status > http.StatusNetworkAuthenticationRequired {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	for _, name := range server.AdminWords.Headers.ForwardResponse {
		for _, value := range result.Headers[name] {
			response.Header().Add(name, value)
		}
	}
	response.WriteHeader(result.Status)
	if request.Method != http.MethodHead {
		_, _ = response.Write(result.Body)
	}
}

func invalidCaddyPathSegment(value, separator string) bool {
	for _, segment := range strings.Split(value, separator) {
		if segment == "." || segment == ".." {
			return true
		}
	}
	return false
}

func containsAnyString(value string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (server *Server) isPluginDetailPath(path string) bool {
	separator := server.Management.Paths.GroupIDSeparator
	if server.Management.Paths.Plugins == "" || separator == "" {
		return false
	}
	prefix := server.Management.Paths.Plugins + separator
	instanceID := strings.TrimPrefix(path, prefix)
	return instanceID != path && instanceID != "" && !strings.Contains(instanceID, separator)
}

func (server *Server) handlePluginDetail(response http.ResponseWriter, path, requestID string) {
	separator := server.Management.Paths.GroupIDSeparator
	instanceID := strings.TrimPrefix(path, server.Management.Paths.Plugins+separator)
	if server.PluginIDField == "" {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}

	var matched map[string]any
	server.mu.RLock()
	for _, item := range server.Plugins {
		plugin, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if candidateID, ok := plugin[server.PluginIDField].(string); ok && candidateID == instanceID {
			matched = plugin
			break
		}
	}
	server.mu.RUnlock()
	if matched == nil {
		server.writeCatalogProblem(response, server.Management.Codes.PluginNotFound, requestID)
		return
	}
	server.writeJSON(response, http.StatusOK, matched)
}

func (server *Server) isPluginCookiePolicyPath(path string) bool {
	separator := server.Management.Paths.GroupIDSeparator
	if separator == "" || server.Management.Paths.PluginCookiePolicies == "" || server.Management.Paths.CookiePoliciesSuffix == "" {
		return false
	}
	prefix := strings.TrimSuffix(server.Management.Paths.PluginCookiePolicies, separator) + separator
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return false
	}
	suffix := server.Management.Paths.CookiePoliciesSuffix
	index := strings.Index(rest, suffix)
	if index <= 0 || !strings.HasPrefix(rest[index:], suffix) {
		return false
	}
	capability := rest[index+len(suffix):]
	return capability != "" && !strings.Contains(capability, separator) && !strings.Contains(rest[:index], separator)
}

func (server *Server) handlePluginCookiePolicy(response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if server.CookiePolicies == nil {
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyUnavailable, requestID)
		return
	}
	instanceID, capability, ok := server.pluginCookiePolicyResource(path)
	if !ok {
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyNotFound, requestID)
		return
	}
	if request.Method == server.Management.Methods.Get {
		policy, err := server.CookiePolicies.Get(request.Context(), instanceID, capability)
		if err != nil {
			server.writeCookiePolicyFailure(response, err, requestID)
			return
		}
		response.Header().Set(server.Management.Headers.ETag, cookiePolicyETag(policy.Revision))
		server.writeJSON(response, http.StatusOK, policy)
		return
	}
	rawETag := request.Header.Get(server.Management.Headers.IfMatch)
	if rawETag == "" {
		server.writeCatalogProblem(response, server.Management.Codes.CookiePolicyPreconditionRequired, requestID)
		return
	}
	expected, valid := parseCookiePolicyETag(rawETag)
	if !valid {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, server.Management.CookiePolicy.MaximumBodyBytes))
	decoder.DisallowUnknownFields()
	var input pluginCookiePolicyInput
	if err := decoder.Decode(&input); err != nil || input.AllowedNames == nil || decoder.Decode(&struct{}{}) != io.EOF {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidCookiePolicy, requestID)
		return
	}
	policy := plugins.CookiePolicy{
		Version: server.Management.CookiePolicy.Version, InstanceID: instanceID,
		Capability: capability, AllowedNames: input.AllowedNames,
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	validated, err := plugins.DecodeCookiePolicy(encoded)
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.InvalidCookiePolicy, requestID)
		return
	}
	result, err := server.CookiePolicies.Replace(request.Context(), expected, models.PluginCookiePolicy{
		InstanceID: validated.InstanceID, Capability: validated.Capability, AllowedNames: validated.AllowedNames,
	}, actor, requestID)
	if err != nil {
		server.writeCookiePolicyFailure(response, err, requestID)
		return
	}
	response.Header().Set(server.Management.Headers.ETag, cookiePolicyETag(result.Revision))
	server.writeJSON(response, http.StatusOK, result)
}

func (server *Server) pluginCookiePolicyResource(path string) (string, string, bool) {
	separator := server.Management.Paths.GroupIDSeparator
	prefix := strings.TrimSuffix(server.Management.Paths.PluginCookiePolicies, separator) + separator
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return "", "", false
	}
	suffix := server.Management.Paths.CookiePoliciesSuffix
	index := strings.Index(rest, suffix)
	if index <= 0 || !strings.HasPrefix(rest[index:], suffix) {
		return "", "", false
	}
	instanceID, capability := rest[:index], rest[index+len(suffix):]
	if capability == "" || strings.Contains(capability, separator) || strings.Contains(instanceID, separator) {
		return "", "", false
	}
	return instanceID, capability, true
}

func cookiePolicyETag(revision int64) string {
	return strconv.Quote(strconv.FormatInt(revision, 10))
}

func parseCookiePolicyETag(value string) (int64, bool) {
	decoded, err := strconv.Unquote(value)
	if err != nil || decoded == "" {
		return 0, false
	}
	revision, err := strconv.ParseInt(decoded, 10, 64)
	return revision, err == nil && revision >= 0 && strconv.FormatInt(revision, 10) == decoded
}

func (server *Server) handleGroupList(response http.ResponseWriter, request *http.Request, requestID string) {
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	groups, err := server.GroupService.List(request.Context())
	if err != nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(groups.Items))
	for _, group := range groups.Items {
		items = append(items, server.groupResponse(group))
	}
	server.writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.Items:     items,
		server.Management.JSON.RequestID: requestID,
	})
}

func (server *Server) handleGroupCreate(response http.ResponseWriter, request *http.Request, requestID, actor string) {
	fail := func(code string) {
		if err := server.recordAudit(request.Context(), actor, server.AuditWords.Audit.Actions.GroupCreate, server.AuditWords.Audit.Resources.Groups, server.AuditWords.Audit.Results.Failed, requestID, "", ""); err != nil {
			server.writeProblem(response, http.StatusServiceUnavailable, server.AuditWords.Audit.StorageUnavailable.Code, server.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		server.writeCatalogProblem(response, code, requestID)
	}
	decoder := json.NewDecoder(request.Body)
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	if len(fields) != 2 {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	for key := range fields {
		if key != server.Management.JSON.ID && key != server.Management.JSON.IdempotencyKey {
			fail(server.Management.Codes.InvalidRequest)
			return
		}
	}
	var id, key string
	if json.Unmarshal(fields[server.Management.JSON.ID], &id) != nil || json.Unmarshal(fields[server.Management.JSON.IdempotencyKey], &key) != nil || len(key) < server.Management.Idempotency.KeyMin || len(key) > server.Management.Idempotency.KeyChars || !ascii(key) {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	validID, err := regexp.MatchString(server.Management.Paths.GroupIDPattern, id)
	if err != nil {
		fail(server.Management.Codes.ManagementUnavailable)
		return
	}
	if !validID {
		fail(server.Management.Codes.InvalidRequest)
		return
	}
	if server.GroupService.Store == nil {
		fail(server.Management.Codes.ManagementUnavailable)
		return
	}
	auditRecord := models.AuditRecord{
		Actor:     actor,
		Action:    server.AuditWords.Audit.Actions.GroupCreate,
		Resource:  server.AuditWords.Audit.Resources.Groups,
		Result:    server.AuditWords.Audit.Results.Succeeded,
		RequestID: requestID,
	}
	group, err := server.GroupService.Create(request.Context(), id, auditRecord)
	if err != nil {
		var auditFailure models.AuditAppendError
		if errors.As(err, &auditFailure) {
			server.writeProblem(response, http.StatusServiceUnavailable, server.AuditWords.Audit.StorageUnavailable.Code, server.AuditWords.Audit.StorageUnavailable.Detail, requestID)
			return
		}
		var exists models.GroupAlreadyExists
		if errors.As(err, &exists) {
			fail(server.Management.Codes.GroupAlreadyExists)
			return
		}
		fail(server.Management.Codes.ManagementUnavailable)
		return
	}
	response.Header().Set(server.Management.Headers.Location, server.Management.Paths.GroupByID+id)
	result := server.groupResponse(group)
	result[server.Management.JSON.RequestID] = requestID
	server.writeJSON(response, http.StatusCreated, result)
}

func (server *Server) handleGroupGet(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	id := strings.TrimPrefix(path, server.Management.Paths.GroupByID)
	if id == "" || strings.Contains(id, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	group, err := server.GroupService.Get(request.Context(), id)
	if err != nil {
		var notFound models.GroupNotFound
		if errors.As(err, &notFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	result := server.groupResponse(group)
	result[server.Management.JSON.RequestID] = requestID
	server.writeJSON(response, http.StatusOK, result)
}

func (server *Server) handleGroupReleases(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.GroupService.Store == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	suffix := server.Management.Paths.GroupIDSeparator + server.Management.Paths.GroupReleases
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.GroupByID), suffix)
	if groupID == "" || strings.Contains(groupID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	limit := 0
	if rawLimit := request.URL.Query().Get(server.Management.JSON.Limit); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
			return
		}
		limit = parsed
	}
	page, err := server.GroupService.ListRevisions(request.Context(), groupID, request.URL.Query().Get(server.Management.JSON.Cursor), limit)
	if err != nil {
		var notFound models.GroupNotFound
		if errors.As(err, &notFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		var invalidPage models.GroupRevisionPageError
		if errors.As(err, &invalidPage) {
			server.writeCatalogProblem(response, server.Management.Codes.InvalidRequest, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, revision := range page.Items {
		items = append(items, map[string]any{
			server.Management.JSON.ID:              revision.ID,
			server.Management.JSON.GroupID:         revision.GroupID,
			server.Management.JSON.CaddyfileDigest: revision.CaddyfileDigest,
			server.Management.JSON.ArtifactDigest:  revision.ArtifactDigest,
			server.Management.JSON.CreatedAt:       revision.CreatedAt,
			server.Management.JSON.Actor:           revision.Actor,
		})
	}
	server.writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.Items:      items,
		server.Management.JSON.NextCursor: page.NextCursor,
		server.Management.JSON.RequestID:  requestID,
	})
}

func (server *Server) handleGroupRelease(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.GroupService.Store == nil || server.GroupService.ContentReader == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	suffix := server.Management.Paths.GroupIDSeparator + server.Management.Paths.GroupReleases + server.Management.Paths.GroupIDSeparator
	resource := strings.TrimPrefix(path, server.Management.Paths.GroupByID)
	groupID, revisionID, found := strings.Cut(resource, suffix)
	if !found || groupID == "" || revisionID == "" || strings.Contains(revisionID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return
	}
	detail, err := server.GroupService.GetRevisionDetail(request.Context(), groupID, revisionID)
	if err != nil {
		var notFound models.GroupRevisionNotFound
		if errors.As(err, &notFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	revision := detail.Revision
	frontends := make([]map[string]any, 0, len(detail.Frontends))
	for _, frontend := range detail.Frontends {
		frontends = append(frontends, map[string]any{
			server.Management.JSON.ID:     frontend.ID,
			server.Management.JSON.Digest: frontend.Digest,
			server.Management.JSON.Files:  frontend.Files,
		})
	}
	server.writeJSON(response, http.StatusOK, map[string]any{
		server.Management.JSON.ID:              revision.ID,
		server.Management.JSON.GroupID:         revision.GroupID,
		server.Management.JSON.Caddyfile:       detail.Caddyfile,
		server.Management.JSON.CaddyfileDigest: revision.CaddyfileDigest,
		server.Management.JSON.ArtifactDigest:  revision.ArtifactDigest,
		server.Management.JSON.Frontends:       frontends,
		server.Management.JSON.CreatedAt:       revision.CreatedAt,
		server.Management.JSON.Actor:           revision.Actor,
		server.Management.JSON.RequestID:       requestID,
	})
}

func (server *Server) handleGroupPublish(response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if server.GroupReleases == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	groupID, metadata, caddyfile, artifact, ok := server.readGroupPublishRequest(response, request, path, requestID)
	if !ok {
		return
	}
	operation, ok := server.acceptGroupPublish(response, request, requestID, actor, groupID, metadata, caddyfile, artifact)
	if !ok {
		return
	}
	server.writeGroupOperationAccepted(response, operation, requestID)
}

func (server *Server) readGroupPublishRequest(response http.ResponseWriter, request *http.Request, path, requestID string) (string, groupReleaseMetadata, []byte, []byte, bool) {
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.GroupByID), server.Management.Paths.GroupIDSeparator+server.Management.Paths.GroupReleases)
	if groupID == "" || strings.Contains(groupID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return "", groupReleaseMetadata{}, nil, nil, false
	}
	contentType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != server.GroupReleasePolicy.MultipartContentType || parameters["boundary"] == "" {
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", groupReleaseMetadata{}, nil, nil, false
	}
	request.Body = http.MaxBytesReader(response, request.Body, server.GroupReleasePolicy.RequestLimitBytes)
	metadata, caddyfile, artifact, err := readGroupReleaseMultipart(multipart.NewReader(request.Body, parameters["boundary"]), server.GroupReleasePolicy, server.Management)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
			return "", groupReleaseMetadata{}, nil, nil, false
		}
		var archiveError models.GroupReleaseArchiveError
		if errors.As(err, &archiveError) {
			if archiveError.TooLarge {
				server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
				return "", groupReleaseMetadata{}, nil, nil, false
			}
			server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactInvalidCode, requestID)
			return "", groupReleaseMetadata{}, nil, nil, false
		}
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", groupReleaseMetadata{}, nil, nil, false
	}
	return groupID, metadata, caddyfile, artifact, true
}

func (server *Server) acceptGroupPublish(response http.ResponseWriter, request *http.Request, requestID, actor, groupID string, metadata groupReleaseMetadata, caddyfile, artifact []byte) (models.Operation, bool) {
	operation, _, err := server.GroupReleases.Accept(request.Context(), models.GroupReleaseCommand{
		GroupID: groupID, Actor: actor, RequestID: requestID,
		IdempotencyKey:          metadata.IdempotencyKey,
		IdempotencyScope:        server.GroupReleasePolicy.ScopePrefix + groupID + server.GroupReleasePolicy.ScopeSuffix,
		ExpectedCurrentRevision: metadata.ExpectedCurrentRevision,
		IdempotencyWindow:       server.GroupReleasePolicy.IdempotencyWindow,
		Caddyfile:               caddyfile, Artifact: artifact,
	})
	if err != nil {
		var driftBlocked models.GroupDriftBlocked
		if errors.As(err, &driftBlocked) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.DriftBlockedCode, requestID)
			return models.Operation{}, false
		}
		var conflict models.GroupRevisionConflict
		if errors.As(err, &conflict) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.RevisionConflictCode, requestID)
			return models.Operation{}, false
		}
		var idempotencyConflict models.IdempotencyConflict
		if errors.As(err, &idempotencyConflict) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.IdempotencyConflictCode, requestID)
			return models.Operation{}, false
		}
		var groupNotFound models.GroupNotFound
		if errors.As(err, &groupNotFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return models.Operation{}, false
		}
		var validation models.GroupReleaseValidationError
		if errors.As(err, &validation) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.CaddyAdaptFailedCode, requestID)
			return models.Operation{}, false
		}
		var archiveError models.GroupReleaseArchiveError
		if errors.As(err, &archiveError) {
			if archiveError.TooLarge {
				server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
				return models.Operation{}, false
			}
			server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactInvalidCode, requestID)
			return models.Operation{}, false
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return models.Operation{}, false
	}
	return operation, true
}

func (server *Server) writeGroupOperationAccepted(response http.ResponseWriter, operation models.Operation, requestID string) {
	state := operation.State
	if state != server.GroupReleasePolicy.PendingState && state != server.GroupReleasePolicy.RunningState {
		state = server.GroupReleasePolicy.PendingState
	}
	server.writeJSON(response, http.StatusAccepted, map[string]any{
		server.Management.JSON.OperationID: operation.ID,
		server.Management.JSON.State:       state,
		server.Management.JSON.RequestID:   requestID,
	})
}

func (server *Server) handleGroupRollback(response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if server.GroupReleases == nil {
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	groupID, idempotencyKey, expected, ok := server.readGroupRollbackRequest(response, request, path, requestID)
	if !ok {
		return
	}
	operation, ok := server.acceptGroupRollback(response, request, requestID, actor, groupID, idempotencyKey, expected)
	if !ok {
		return
	}
	server.writeGroupOperationAccepted(response, operation, requestID)
}

func (server *Server) readGroupRollbackRequest(response http.ResponseWriter, request *http.Request, path, requestID string) (string, string, *string, bool) {
	groupID := strings.TrimSuffix(strings.TrimPrefix(path, server.Management.Paths.GroupByID), server.Management.Paths.GroupIDSeparator+server.Management.Paths.GroupRollback)
	if groupID == "" || strings.Contains(groupID, server.Management.Paths.GroupIDSeparator) {
		server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
		return "", "", nil, false
	}
	contentType, _, contentTypeErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if contentTypeErr != nil || contentType != server.GroupReleasePolicy.MetadataContentType {
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", "", nil, false
	}
	request.Body = http.MaxBytesReader(response, request.Body, server.GroupReleasePolicy.RequestLimitBytes)
	fields := make(map[string]json.RawMessage)
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&fields); err != nil || fields == nil || len(fields) != 2 {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.ArtifactTooLargeCode, requestID)
			return "", "", nil, false
		}
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", "", nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", "", nil, false
	}
	keyJSON, hasKey := fields[server.Management.JSON.IdempotencyKey]
	expectedJSON, hasExpected := fields[server.GroupReleasePolicy.ExpectedCurrentRevisionField]
	var idempotencyKey string
	var expected *string
	if !hasKey || !hasExpected || json.Unmarshal(keyJSON, &idempotencyKey) != nil || len(idempotencyKey) < server.Management.Idempotency.KeyMin || len(idempotencyKey) > server.Management.Idempotency.KeyChars || !ascii(idempotencyKey) || json.Unmarshal(expectedJSON, &expected) != nil {
		server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
		return "", "", nil, false
	}
	if expected != nil {
		valid, err := regexp.MatchString(server.GroupReleasePolicy.RevisionIDPattern, *expected)
		if err != nil || !valid {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.InvalidRequestCode, requestID)
			return "", "", nil, false
		}
	}
	return groupID, idempotencyKey, expected, true
}

func (server *Server) acceptGroupRollback(response http.ResponseWriter, request *http.Request, requestID, actor, groupID, idempotencyKey string, expected *string) (models.Operation, bool) {
	operation, _, err := server.GroupReleases.Rollback(request.Context(), models.GroupRollbackCommand{
		GroupID: groupID, Actor: actor, RequestID: requestID,
		IdempotencyKey:          idempotencyKey,
		IdempotencyScope:        server.GroupReleasePolicy.ScopePrefix + groupID + server.GroupReleasePolicy.RollbackScopeSuffix,
		ExpectedCurrentRevision: expected, IdempotencyWindow: server.GroupReleasePolicy.IdempotencyWindow,
	})
	if err != nil {
		var conflict models.GroupRevisionConflict
		if errors.As(err, &conflict) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.RevisionConflictCode, requestID)
			return models.Operation{}, false
		}
		var idempotencyConflict models.IdempotencyConflict
		if errors.As(err, &idempotencyConflict) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.IdempotencyConflictCode, requestID)
			return models.Operation{}, false
		}
		var groupNotFound models.GroupNotFound
		if errors.As(err, &groupNotFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return models.Operation{}, false
		}
		var revisionNotFound models.GroupRevisionNotFound
		if errors.As(err, &revisionNotFound) {
			server.writeCatalogProblem(response, server.Management.Codes.GroupNotFound, requestID)
			return models.Operation{}, false
		}
		var validation models.GroupReleaseValidationError
		if errors.As(err, &validation) {
			server.writeCatalogProblem(response, server.GroupReleasePolicy.CaddyAdaptFailedCode, requestID)
			return models.Operation{}, false
		}
		server.writeCatalogProblem(response, server.Management.Codes.ManagementUnavailable, requestID)
		return models.Operation{}, false
	}
	return operation, true
}

func (server *Server) handlePluginAdmin(response http.ResponseWriter, request *http.Request, path, requestID string) {
	if server.AdminDispatcher == nil {
		server.writeProblem(response, 503, "plugin_unavailable", "plugin admin surface is unavailable", requestID)
		return
	}
	instanceAndRoute := strings.TrimPrefix(path, server.Management.Paths.Plugins+"/")
	instance, route, found := strings.Cut(instanceAndRoute, "/")
	pagePrefix := server.Management.Paths.AdminPages + "/"
	if !found || instance == "" || !strings.HasPrefix(route, pagePrefix) {
		server.writeProblem(response, 404, "not_found", "plugin admin resource not found", requestID)
		return
	}
	pageAndAction := strings.TrimPrefix(route, pagePrefix)
	parts := strings.Split(pageAndAction, "/")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		server.writeProblem(response, 404, "not_found", "plugin admin resource not found", requestID)
		return
	}
	page := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	var input json.RawMessage
	if request.Method == http.MethodPost {
		defer request.Body.Close()
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil && err.Error() != "EOF" {
			server.writeProblem(response, 400, "invalid_input", "request body must be JSON", requestID)
			return
		}
	}
	result, err := server.AdminDispatcher.Dispatch(request.Context(), instance, plugins.RequestContext{Instance: instance, Page: page, Action: action, Method: request.Method, RequestID: requestID, Actor: "management", Input: input})
	if err != nil {
		server.writeProblem(response, 502, "plugin_error", err.Error(), requestID)
		return
	}
	if result.Status == 0 {
		result.Status = 200
	}
	if result.ContentType == "" {
		result.ContentType = server.Management.ContentTypes.JSON
	}
	response.Header().Set("Content-Type", result.ContentType)
	response.WriteHeader(result.Status)
	_, _ = response.Write(result.Body)
}
func randomID() string {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(bytes)
}
func (server *Server) authenticate(ctx context.Context, value string) (string, bool, error) {
	if !strings.HasPrefix(value, "Bearer ") {
		return "", false, nil
	}
	key := strings.TrimPrefix(value, "Bearer ")
	if server.AccessService != nil {
		actor, valid, err := server.AccessService.Authenticate(ctx, key)
		return actor, valid, err
	}
	if server.Token != "" && key == server.Token {
		return server.AuditWords.Audit.Actors.StaticToken, true, nil
	}
	for _, account := range server.ServiceAccounts {
		if bcrypt.CompareHashAndPassword([]byte(account.KeyHash), []byte(key)) == nil {
			return account.ID, true, nil
		}
	}
	return "", false, nil
}
