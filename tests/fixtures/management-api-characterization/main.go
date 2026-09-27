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
)

type requestCase struct {
	name   string
	method string
	path   string
	token  string
	body   string
	mtls   bool
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
	adminWords, err := config.LoadAdminMutation()
	if err != nil {
		panic(err)
	}
	server := &api.Server{
		Token:           "characterization-token",
		Management:      management,
		AuditWords:      auditWords,
		AdminWords:      adminWords,
		CaddyVariant:    "embedded",
		CaddyBuildID:    "fixture-build",
		CaddyModules:    []string{"http", "layer4"},
		DataPlaneState:  management.Statuses.NotReady,
		DataPlaneReason: management.Statuses.SystemReleaseRequired,
		Plugins: []any{
			map[string]any{"id": "fixture-a", "name": "Alpha", "state": "ready"},
			map[string]any{"id": "fixture-b", "name": "Beta", "state": "starting"},
		},
		PluginIDField: "id",
		AdminSurfaces: []api.AdminSurface{{
			Plugin: "fixture-a", Namespace: "forms", Version: "v1", Title: "Forms",
			Capabilities: []string{"admin.surface.get"},
		}},
	}
	mtlsServer := &api.Server{
		Token:                    "characterization-token",
		Management:               management,
		AuditWords:               auditWords,
		AdminWords:               adminWords,
		RequireClientCertificate: true,
	}
	serverForRequests := httptest.NewServer(server.Handler())
	defer serverForRequests.Close()
	serverForMTLS := httptest.NewServer(mtlsServer.Handler())
	defer serverForMTLS.Close()

	group := management.Paths.GroupByID + "missing"
	plugin := management.Paths.Plugins + "/fixture-a"
	cases := []requestCase{
		{name: "healthGet", method: http.MethodGet, path: management.Paths.Healthz},
		{name: "healthHead", method: http.MethodHead, path: management.Paths.Healthz},
		{name: "healthPost", method: http.MethodPost, path: management.Paths.Healthz},
		{name: "statusUnauthorized", method: http.MethodGet, path: management.Paths.Status},
		{name: "statusAuthenticated", method: http.MethodGet, path: management.Paths.Status, token: "characterization-token"},
		{name: "statusMTLSRequired", method: http.MethodGet, path: management.Paths.Status, token: "characterization-token", mtls: true},
		{name: "healthBeforeMTLS", method: http.MethodGet, path: management.Paths.Healthz, mtls: true},
		{name: "groupListUnavailable", method: http.MethodGet, path: management.Paths.Groups, token: "characterization-token"},
		{name: "groupDetailUnavailable", method: http.MethodGet, path: group, token: "characterization-token"},
		{name: "groupReleaseListUnavailable", method: http.MethodGet, path: group + "/" + management.Paths.GroupReleases, token: "characterization-token"},
		{name: "groupReleaseDetailUnavailable", method: http.MethodGet, path: group + "/" + management.Paths.GroupReleases + "/revision", token: "characterization-token"},
		{name: "groupPublishUnavailable", method: http.MethodPost, path: group + "/" + management.Paths.GroupReleases, token: "characterization-token"},
		{name: "groupRollbackUnavailable", method: http.MethodPost, path: group + "/" + management.Paths.GroupRollback, token: "characterization-token"},
		{name: "serviceKeyListUnavailable", method: http.MethodGet, path: management.Paths.ServiceKeys, token: "characterization-token"},
		{name: "serviceKeyCreateUnavailable", method: http.MethodPost, path: management.Paths.ServiceKeys, token: "characterization-token", body: `{}`},
		{name: "pluginList", method: http.MethodGet, path: management.Paths.Plugins + "?limit=1", token: "characterization-token"},
		{name: "invalidPluginPagination", method: http.MethodGet, path: management.Paths.Plugins + "?limit=0", token: "characterization-token"},
		{name: "adminSurfaces", method: http.MethodGet, path: management.Paths.AdminSurfaces, token: "characterization-token"},
		{name: "pluginDetail", method: http.MethodGet, path: plugin, token: "characterization-token"},
		{name: "pluginNotFound", method: http.MethodGet, path: management.Paths.Plugins + "/missing", token: "characterization-token"},
		{name: "cookiePolicyUnavailable", method: http.MethodGet, path: management.Paths.PluginCookiePolicies + "/fixture-a" + management.Paths.CookiePoliciesSuffix + "forms.submit", token: "characterization-token"},
		{name: "pluginAdminUnavailable", method: http.MethodGet, path: plugin + "/" + management.Paths.AdminPages + "/overview", token: "characterization-token"},
		{name: "pluginAdminPostUnavailable", method: http.MethodPost, path: plugin + "/" + management.Paths.AdminPages + "/records/delete", token: "characterization-token", body: `{}`},
		{name: "pluginRestartUnavailable", method: http.MethodPost, path: plugin + "/" + management.Paths.Restart, token: "characterization-token"},
		{name: "caddyAdminUnavailable", method: http.MethodGet, path: strings.TrimSuffix(adminWords.Paths.ManagementPrefix, "/") + "/config/", token: "characterization-token"},
		{name: "operationUnavailable", method: http.MethodGet, path: management.Paths.Operations + "/missing", token: "characterization-token"},
		{name: "auditEmpty", method: http.MethodGet, path: management.Paths.Audit, token: "characterization-token"},
		{name: "unknownRoute", method: http.MethodGet, path: "/api/unknown", token: "characterization-token"},
		{name: "retiredConfigRoute", method: http.MethodGet, path: "/api/config", token: "characterization-token"},
		{name: "unsupportedMethod", method: http.MethodDelete, path: management.Paths.Groups, token: "characterization-token"},
	}

	client := serverForRequests.Client()
	mtlsClient := serverForMTLS.Client()
	result := make(map[string]observation, len(cases))
	for _, testCase := range cases {
		requestClient := client
		requestServer := serverForRequests
		if testCase.mtls {
			requestClient = mtlsClient
			requestServer = serverForMTLS
		}
		result[testCase.name] = perform(requestClient, requestServer.URL, testCase, management.Headers.RequestID)
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
