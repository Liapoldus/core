package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type pluginCookiePolicyInput struct {
	AllowedNames []string `json:"allowedNames"`
}

func PluginList(deps PluginDependencies, response http.ResponseWriter, request *http.Request, requestID string) {
	deps.WritePage(response, deps.Plugins, request, requestID)
}

func AdminSurfaceList(deps PluginDependencies, response http.ResponseWriter, requestID string) {
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Items:     deps.AdminSurfaces,
		deps.Management.JSON.RequestID: requestID,
	})
}

func PluginRestart(deps PluginDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.RestartPlugin == nil {
		deps.WriteProblem(response, 501, "not_implemented", "plugin restart is unavailable", requestID)
		return
	}
	instance := strings.TrimSuffix(strings.TrimPrefix(path, deps.Management.Paths.Plugins+"/"), "/"+deps.Management.Paths.Restart)
	if len(instance) == 0 || strings.Contains(instance, "/") {
		deps.WriteProblem(response, http.StatusNotFound, "not_found", "plugin resource not found", requestID)
		return
	}
	if deps.Operations.Store == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	op, err := deps.RestartPlugin(request.Context(), instance)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	if op.ID == "" || op.Kind == "" || (op.State != deps.Management.Statuses.Pending && op.State != deps.Management.Statuses.Running) {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	op.RequestID = requestID
	op.Actor = actor
	op.Resource = instance
	if op.CreatedAt.IsZero() {
		op.CreatedAt = time.Now().UTC()
	}
	if err := deps.Operations.Create(request.Context(), op); err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	deps.WriteJSON(response, http.StatusAccepted, map[string]any{
		deps.Management.JSON.OperationID: op.ID,
		deps.Management.JSON.State:       op.State,
		deps.Management.JSON.RequestID:   requestID,
	})
}

func IsPluginDetailPath(deps PluginDependencies, path string) bool {
	separator := deps.Management.Paths.GroupIDSeparator
	if deps.Management.Paths.Plugins == "" || separator == "" {
		return false
	}
	prefix := deps.Management.Paths.Plugins + separator
	instanceID := strings.TrimPrefix(path, prefix)
	return instanceID != path && instanceID != "" && !strings.Contains(instanceID, separator)
}

func PluginDetail(deps PluginDependencies, response http.ResponseWriter, path, requestID string) {
	separator := deps.Management.Paths.GroupIDSeparator
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

func IsPluginCookiePolicyPath(deps PluginDependencies, path string) bool {
	separator := deps.Management.Paths.GroupIDSeparator
	if separator == "" || deps.Management.Paths.PluginCookiePolicies == "" || deps.Management.Paths.CookiePoliciesSuffix == "" {
		return false
	}
	prefix := strings.TrimSuffix(deps.Management.Paths.PluginCookiePolicies, separator) + separator
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return false
	}
	suffix := deps.Management.Paths.CookiePoliciesSuffix
	index := strings.Index(rest, suffix)
	if index <= 0 || !strings.HasPrefix(rest[index:], suffix) {
		return false
	}
	capability := rest[index+len(suffix):]
	return capability != "" && !strings.Contains(capability, separator) && !strings.Contains(rest[:index], separator)
}

func PluginCookiePolicy(deps PluginDependencies, response http.ResponseWriter, request *http.Request, path, requestID, actor string) {
	if deps.CookiePolicies == nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.CookiePolicyUnavailable, requestID)
		return
	}
	instanceID, capability, ok := pluginCookiePolicyResource(deps, path)
	if !ok {
		deps.WriteCatalogProblem(response, deps.Management.Codes.CookiePolicyNotFound, requestID)
		return
	}
	if request.Method == deps.Management.Methods.Get {
		policy, err := deps.CookiePolicies.Get(request.Context(), instanceID, capability)
		if err != nil {
			deps.WriteCookiePolicyError(response, err, requestID)
			return
		}
		response.Header().Set(deps.Management.Headers.ETag, cookiePolicyETag(policy.Revision))
		deps.WriteJSON(response, http.StatusOK, policy)
		return
	}
	rawETag := request.Header.Get(deps.Management.Headers.IfMatch)
	if rawETag == "" {
		deps.WriteCatalogProblem(response, deps.Management.Codes.CookiePolicyPreconditionRequired, requestID)
		return
	}
	expected, valid := parseCookiePolicyETag(rawETag)
	if !valid {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidRequest, requestID)
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, deps.Management.CookiePolicy.MaximumBodyBytes))
	decoder.DisallowUnknownFields()
	var input pluginCookiePolicyInput
	if err := decoder.Decode(&input); err != nil || input.AllowedNames == nil || decoder.Decode(&struct{}{}) != io.EOF {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidCookiePolicy, requestID)
		return
	}
	policy := struct {
		Version      int      `json:"version"`
		InstanceID   string   `json:"instanceId"`
		Capability   string   `json:"capability"`
		AllowedNames []string `json:"allowedNames"`
	}{
		Version: deps.CookiePolicyVersion, InstanceID: instanceID,
		Capability: capability, AllowedNames: input.AllowedNames,
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.ManagementUnavailable, requestID)
		return
	}
	validated, err := deps.DecodeCookiePolicy(encoded)
	if err != nil {
		deps.WriteCatalogProblem(response, deps.Management.Codes.InvalidCookiePolicy, requestID)
		return
	}
	result, err := deps.CookiePolicies.Replace(request.Context(), expected, validated, actor, requestID)
	if err != nil {
		deps.WriteCookiePolicyError(response, err, requestID)
		return
	}
	response.Header().Set(deps.Management.Headers.ETag, cookiePolicyETag(result.Revision))
	deps.WriteJSON(response, http.StatusOK, result)
}

func pluginCookiePolicyResource(deps PluginDependencies, path string) (string, string, bool) {
	separator := deps.Management.Paths.GroupIDSeparator
	prefix := strings.TrimSuffix(deps.Management.Paths.PluginCookiePolicies, separator) + separator
	rest := strings.TrimPrefix(path, prefix)
	if rest == path {
		return "", "", false
	}
	suffix := deps.Management.Paths.CookiePoliciesSuffix
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

func PluginAdmin(deps PluginDependencies, response http.ResponseWriter, request *http.Request, path, requestID string) {
	if deps.DispatchAdmin == nil {
		deps.WriteProblem(response, 503, "plugin_unavailable", "plugin admin surface is unavailable", requestID)
		return
	}
	instanceAndRoute := strings.TrimPrefix(path, deps.Management.Paths.Plugins+"/")
	instance, route, found := strings.Cut(instanceAndRoute, "/")
	pagePrefix := deps.Management.Paths.AdminPages + "/"
	if !found || instance == "" || !strings.HasPrefix(route, pagePrefix) {
		deps.WriteProblem(response, 404, "not_found", "plugin admin resource not found", requestID)
		return
	}
	pageAndAction := strings.TrimPrefix(route, pagePrefix)
	parts := strings.Split(pageAndAction, "/")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && parts[1] == "") {
		deps.WriteProblem(response, 404, "not_found", "plugin admin resource not found", requestID)
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
			deps.WriteProblem(response, 400, "invalid_input", "request body must be JSON", requestID)
			return
		}
	}
	result, err := deps.DispatchAdmin(request.Context(), instance, page, action, request.Method, requestID, "management", input)
	if err != nil {
		deps.WriteProblem(response, 502, "plugin_error", err.Error(), requestID)
		return
	}
	if result.Status == 0 {
		result.Status = 200
	}
	if result.ContentType == "" {
		result.ContentType = deps.Management.ContentTypes.JSON
	}
	response.Header().Set("Content-Type", result.ContentType)
	response.WriteHeader(result.Status)
	_, _ = response.Write(result.Body)
}
