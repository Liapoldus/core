// Package api adapts the Management API to application use cases.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
