package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
	"github.com/Liapoldus/core/v3/internal/presentation/api"
)

type observation struct {
	Status    int    `json:"status"`
	ETag      string `json:"etag"`
	Code      string `json:"code"`
	Revision  int64  `json:"revision"`
	ItemCount int    `json:"itemCount"`
}

type requestCase struct {
	name         string
	method       string
	path         string
	idempotency  string
	ifMatch      string
	body         string
	observation  string
	skipAuth     bool
	expectPolicy bool
}

func main() {
	ctx := context.Background()
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqliteContract.Driver, ParentDirectoryMode: sqliteContract.ParentDirectoryMode,
		DatabaseFileMode: sqliteContract.DatabaseFileMode, MaxOpenConnections: sqliteContract.MaxOpenConnections,
		MaxIdleConnections: sqliteContract.MaxIdleConnections, SchemaVersion: sqliteContract.SchemaVersion,
		HasMigrationTableQuery: sqliteContract.HasMigrationTableQuery, MigrationVersionQuery: sqliteContract.MigrationVersionQuery,
		SchemaVersionError: sqliteContract.SchemaVersionError, Pragmas: sqliteContract.Pragmas,
	}, sqliteContract.Schema)
	if err != nil {
		panic(err)
	}
	defer database.Close()
	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	auditWords, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	errorCatalog, err := config.LoadErrorCatalog()
	if err != nil {
		panic(err)
	}
	operationStore, err := storage.NewSQLiteOperationStore(database)
	if err != nil {
		panic(err)
	}
	linkStore, err := storage.NewSQLitePluginLinkPolicyStore(database)
	if err != nil {
		panic(err)
	}
	server := &api.Server{
		Token: "link-token", Management: management, AuditWords: auditWords, Errors: errorCatalog,
		Operations: application.OperationService{Store: operationStore},
		PluginLinks: &application.PluginLinkPolicyService{
			Store: linkStore, Reader: linkStore, Operations: operationStore,
			Kinds: application.PluginLinkPolicyOperationKinds{
				Create:  management.OperationKinds.PluginLinkCreate,
				Replace: management.OperationKinds.PluginLinkReplace,
				Delete:  management.OperationKinds.PluginLinkDelete,
			},
			States: application.PluginLinkPolicyOperationStates{
				Pending: management.Statuses.Pending, Succeeded: management.Statuses.Succeeded, Failed: management.Statuses.Failed,
			},
			Now: time.Now,
		},
	}
	serverForRequests := httptest.NewServer(server.Handler())
	defer serverForRequests.Close()

	collection := management.Paths.PluginLinks
	pair := collection + management.Paths.PluginIDSeparator + "caller-a" + management.Paths.PluginIDSeparator + "target-b"
	missing := collection + management.Paths.PluginIDSeparator + "caller-x" + management.Paths.PluginIDSeparator + "target-y"

	validBody := `{"callerInstanceId":"caller-a","targetInstanceId":"target-b","rules":[{"placementRule":"remote","carrier":"tcp","weight":5,"requiredContracts":[{"contractId":"example.contract","minimumVersion":"1.0.0","maximumVersionExclusive":"2.0.0"}]}]}`
	replaceBody := `{"rules":[{"placementRule":"same-placement","carrier":"unix","weight":3,"requiredContracts":[]}]}`

	cases := []requestCase{
		{name: "listEmpty", method: http.MethodGet, path: collection, observation: "listEmpty"},
		{name: "createNoIdempotency", method: http.MethodPost, path: collection, body: validBody, observation: "createNoIdempotency"},
		{name: "createInvalidShape", method: http.MethodPost, path: collection, idempotency: "link-key-bad", body: `{"callerInstanceId":"caller-a","targetInstanceId":"target-b","rules":[]}`, observation: "createInvalidShape"},
		{name: "createOk", method: http.MethodPost, path: collection, idempotency: "link-key-create", body: validBody, observation: "createOk", expectPolicy: true},
		{name: "createReplay", method: http.MethodPost, path: collection, idempotency: "link-key-create", body: validBody, observation: "createReplay", expectPolicy: true},
		{name: "createDuplicate", method: http.MethodPost, path: collection, idempotency: "link-key-duplicate", body: validBody, observation: "createDuplicate"},
		{name: "listOne", method: http.MethodGet, path: collection, observation: "listOne"},
		{name: "getOk", method: http.MethodGet, path: pair, observation: "getOk", expectPolicy: true},
		{name: "getMissing", method: http.MethodGet, path: missing, observation: "getMissing"},
		{name: "replaceStale", method: http.MethodPut, path: pair, idempotency: "link-key-stale-replace", ifMatch: `"9"`, body: replaceBody, observation: "replaceStale"},
		{name: "replaceOk", method: http.MethodPut, path: pair, idempotency: "link-key-replace", ifMatch: `"1"`, body: replaceBody, observation: "replaceOk", expectPolicy: true},
		{name: "replaceMissing", method: http.MethodPut, path: missing, idempotency: "link-key-replace-missing", ifMatch: `"1"`, body: replaceBody, observation: "replaceMissing"},
		{name: "deleteStale", method: http.MethodDelete, path: pair, idempotency: "link-key-stale-delete", ifMatch: `"1"`, observation: "deleteStale"},
		{name: "deleteOk", method: http.MethodDelete, path: pair, idempotency: "link-key-delete", ifMatch: `"2"`, observation: "deleteOk"},
		{name: "deleteMissing", method: http.MethodDelete, path: pair, idempotency: "link-key-delete-missing", ifMatch: `"2"`, observation: "deleteMissing"},
		{name: "listPaginationInvalid", method: http.MethodGet, path: collection + "?limit=0", observation: "listPaginationInvalid"},
	}

	client := serverForRequests.Client()
	result := make(map[string]observation, len(cases))
	for _, testCase := range cases {
		result[testCase.observation] = perform(client, serverForRequests.URL, management, testCase)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}

func perform(client *http.Client, baseURL string, management config.ManagementWords, testCase requestCase) observation {
	var body io.Reader
	if testCase.body != "" {
		body = strings.NewReader(testCase.body)
	}
	request, err := http.NewRequest(testCase.method, baseURL+testCase.path, body)
	if err != nil {
		panic(err)
	}
	if !testCase.skipAuth {
		request.Header.Set("Authorization", "Bearer link-token")
	}
	if testCase.body != "" {
		request.Header.Set(management.Headers.ContentType, management.ContentTypes.JSON)
	}
	if testCase.idempotency != "" {
		request.Header.Set(management.Idempotency.Key, testCase.idempotency)
	}
	if testCase.ifMatch != "" {
		request.Header.Set(management.Headers.IfMatch, testCase.ifMatch)
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
	observation := observation{Status: response.StatusCode, ETag: response.Header.Get(management.Headers.ETag)}
	if len(contents) > 0 {
		decoded := map[string]any{}
		if err := json.Unmarshal(contents, &decoded); err != nil {
			panic(err)
		}
		if code, ok := decoded[management.JSON.ErrorCode].(string); ok {
			observation.Code = code
		}
		if code, ok := decoded["code"].(string); ok {
			observation.Code = code
		}
		if revision, ok := decoded[management.JSON.Revision].(float64); ok {
			observation.Revision = int64(revision)
		}
		if items, ok := decoded[management.JSON.Items].([]any); ok {
			observation.ItemCount = len(items)
		}
	}
	return observation
}
