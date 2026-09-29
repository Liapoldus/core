package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

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

func AdminSurfaceList(deps PluginDependencies, response http.ResponseWriter, requestID string) {
	deps.WriteJSON(response, http.StatusOK, map[string]any{
		deps.Management.JSON.Items:     deps.AdminSurfaces,
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
