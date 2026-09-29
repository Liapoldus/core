package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/presentation/api"
	"github.com/Liapoldus/core/internal/presentation/api/handlers"
)

type requestCase struct {
	name   string
	method string
	path   string
	token  string
	body   string
}

type observation struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType"`
	RequestID   bool   `json:"requestID"`
	Body        any    `json:"body"`
}

func main() {
	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	auditWords, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	server := &api.Server{
		Token:          "characterization-token",
		Management:     management,
		AuditWords:     auditWords,
		DataPlaneState: management.Statuses.Ready,
		Plugins: []any{
			map[string]any{"id": "fixture-a", "name": "Alpha", "state": "ready"},
			map[string]any{"id": "fixture-b", "name": "Beta", "state": "starting"},
		},
		PluginIDField: "id",
		AdminSurfaces: []handlers.AdminSurface{{
			Plugin: "fixture-a", Namespace: "forms", Version: "v1", Title: "Forms",
			Capabilities: []string{"admin.surface.get"},
		}},
	}
	serverForRequests := httptest.NewServer(server.Handler())
	defer serverForRequests.Close()

	plugin := management.Paths.Plugins + "/fixture-a"
	cases := []requestCase{
		{name: "healthGet", method: http.MethodGet, path: management.Paths.Healthz},
		{name: "healthHead", method: http.MethodHead, path: management.Paths.Healthz},
		{name: "healthPost", method: http.MethodPost, path: management.Paths.Healthz},
		{name: "statusUnauthorized", method: http.MethodGet, path: management.Paths.Status},
		{name: "statusAuthenticated", method: http.MethodGet, path: management.Paths.Status, token: "characterization-token"},
		{name: "healthWithoutBearer", method: http.MethodGet, path: management.Paths.Healthz},
		{name: "serviceKeyListUnavailable", method: http.MethodGet, path: management.Paths.ServiceKeys, token: "characterization-token"},
		{name: "serviceKeyCreateUnavailable", method: http.MethodPost, path: management.Paths.ServiceKeys, token: "characterization-token", body: `{}`},
		{name: "pluginList", method: http.MethodGet, path: management.Paths.Plugins + "?limit=1", token: "characterization-token"},
		{name: "invalidPluginPagination", method: http.MethodGet, path: management.Paths.Plugins + "?limit=0", token: "characterization-token"},
		{name: "adminSurfaces", method: http.MethodGet, path: management.Paths.AdminSurfaces, token: "characterization-token"},
		{name: "pluginDetail", method: http.MethodGet, path: plugin, token: "characterization-token"},
		{name: "pluginNotFound", method: http.MethodGet, path: management.Paths.Plugins + "/missing", token: "characterization-token"},
		{name: "pluginAdminUnavailable", method: http.MethodGet, path: plugin + "/" + management.Paths.AdminPages + "/overview", token: "characterization-token"},
		{name: "pluginAdminPostUnavailable", method: http.MethodPost, path: plugin + "/" + management.Paths.AdminPages + "/records/delete", token: "characterization-token", body: `{}`},
		{name: "pluginRollbackUnavailable", method: http.MethodPost, path: plugin + "/" + management.Paths.PluginRollbackSuffix, token: "characterization-token"},
		{name: "operationUnavailable", method: http.MethodGet, path: management.Paths.Operations + "/missing", token: "characterization-token"},
		{name: "auditEmpty", method: http.MethodGet, path: management.Paths.Audit, token: "characterization-token"},
		{name: "unknownRoute", method: http.MethodGet, path: "/api/unknown", token: "characterization-token"},
		{name: "retiredConfigRoute", method: http.MethodGet, path: "/api/config", token: "characterization-token"},
		{name: "unsupportedPluginMethod", method: http.MethodDelete, path: management.Paths.Plugins, token: "characterization-token"},
	}
	if len(os.Args) > 1 && os.Args[1] == "route-matrix" {
		cases = []requestCase{
			{name: "pluginListPost", method: http.MethodPost, path: management.Paths.Plugins, token: "characterization-token"},
			{name: "pluginDetailPut", method: http.MethodPut, path: plugin, token: "characterization-token"},
			{name: "pluginAdminPatch", method: http.MethodPatch, path: plugin + "/" + management.Paths.AdminPages + "/overview", token: "characterization-token"},
			{name: "pluginRollbackGet", method: http.MethodGet, path: plugin + "/" + management.Paths.PluginRollbackSuffix, token: "characterization-token"},
			{name: "adminSurfacesPost", method: http.MethodPost, path: management.Paths.AdminSurfaces, token: "characterization-token"},
			{name: "serviceKeyDelete", method: http.MethodDelete, path: management.Paths.ServiceKeys, token: "characterization-token"},
			{name: "auditPost", method: http.MethodPost, path: management.Paths.Audit, token: "characterization-token"},
			{name: "operationPost", method: http.MethodPost, path: management.Paths.Operations + "/missing", token: "characterization-token"},
			{name: "statusHead", method: http.MethodHead, path: management.Paths.Status, token: "characterization-token"},
		}
	}

	client := serverForRequests.Client()
	result := make(map[string]observation, len(cases))
	for _, testCase := range cases {
		result[testCase.name] = perform(client, serverForRequests.URL, testCase, management.Headers.RequestID)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}

func perform(client *http.Client, baseURL string, testCase requestCase, requestIDHeader string) observation {
	var body io.Reader
	if testCase.body != "" {
		body = strings.NewReader(testCase.body)
	}
	request, err := http.NewRequest(testCase.method, baseURL+testCase.path, body)
	if err != nil {
		panic(err)
	}
	if testCase.token != "" {
		request.Header.Set("Authorization", "Bearer "+testCase.token)
	}
	if testCase.body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		panic(err)
	}
	var decoded any
	if len(contents) > 0 {
		if err := json.Unmarshal(contents, &decoded); err != nil {
			panic(err)
		}
		if values, ok := decoded.(map[string]any); ok {
			if requestID, present := values["requestId"]; present && requestID != "" {
				values["requestId"] = "<request-id>"
			}
		}
	}
	return observation{
		Status: response.StatusCode, ContentType: response.Header.Get("Content-Type"),
		RequestID: response.Header.Get(requestIDHeader) != "", Body: decoded,
	}
}
