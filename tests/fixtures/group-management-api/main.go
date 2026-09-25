package main

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type groupView struct {
	ID               string  `json:"id"`
	Kind             string  `json:"kind"`
	Active           bool    `json:"active"`
	CurrentRevision  *string `json:"currentRevision"`
	PreviousRevision *string `json:"previousRevision"`
	State            string  `json:"state"`
}

type report struct {
	ListStatus                int              `json:"listStatus"`
	ListRequestID             bool             `json:"listRequestID"`
	Groups                    []groupView      `json:"groups"`
	GetStatus                 int              `json:"getStatus"`
	GetRequestID              bool             `json:"getRequestID"`
	GetGroup                  groupView        `json:"getGroup"`
	MissingStatus             int              `json:"missingStatus"`
	MissingProblem            problemView      `json:"missingProblem"`
	UnauthorizedStatus        int              `json:"unauthorizedStatus"`
	ReleaseListStatus         int              `json:"releaseListStatus"`
	ReleaseListRequestID      bool             `json:"releaseListRequestID"`
	Releases                  []map[string]any `json:"releases"`
	ReleasePathsHidden        bool             `json:"releasePathsHidden"`
	ReleaseDetailStatus       int              `json:"releaseDetailStatus"`
	ReleaseDetailSafe         bool             `json:"releaseDetailSafe"`
	PublishStatus             int              `json:"publishStatus"`
	Publish                   map[string]any   `json:"publish"`
	PublishRetryStatus        int              `json:"publishRetryStatus"`
	PublishRetry              map[string]any   `json:"publishRetry"`
	IdempotencyConflictStatus int              `json:"idempotencyConflictStatus"`
	IdempotencyConflictCode   string           `json:"idempotencyConflictCode"`
	InvalidCaddyfileStatus    int              `json:"invalidCaddyfileStatus"`
	InvalidCaddyfileCode      string           `json:"invalidCaddyfileCode"`
	PointersAfterInvalid      *string          `json:"pointersAfterInvalid"`
	StaleRevisionStatus       int              `json:"staleRevisionStatus"`
	StaleRevisionCode         string           `json:"staleRevisionCode"`
	PointersAfterStale        *string          `json:"pointersAfterStale"`
}

type problemView struct {
	Code          string `json:"code"`
	Status        int    `json:"status"`
	RequestID     bool   `json:"requestId"`
	NoStoreDetail bool   `json:"noStoreDetail"`
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
	store, err := storage.NewSQLiteGroupStore(database)
	if err != nil {
		panic(err)
	}
	if _, err := store.CreateApplicationGroup(ctx, "application-a", models.AuditRecord{Actor: "fixture", Action: "group.create", Resource: "groups", Result: "succeeded", RequestID: "fixture-request"}); err != nil {
		panic(err)
	}
	if _, err := store.CreateRevision(ctx, models.GroupRevision{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", GroupID: "application-a", CaddyfileDigest: "687a79b127387ef55ac664491ab82b5665df6e3d9dc62e2b6062485e2da1c9c4", CaddyfilePath: "revision-a.caddyfile",
	}); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(os.Args[1]), "revision-a.caddyfile"), []byte("example.test { respond 200 }\n"), 0o600); err != nil {
		panic(err)
	}
	if _, err := store.AdvanceCurrent(ctx, "application-a", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil); err != nil {
		panic(err)
	}
	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	errorCatalog, err := config.LoadErrorCatalog()
	if err != nil {
		panic(err)
	}
	server := &api.Server{
		Token: "fixture-management-token", Management: management, Errors: errorCatalog,
		GroupService: application.GroupService{
			Store: store, ContentReader: artifacts.GroupRevisionReader{Root: filepath.Dir(os.Args[1])},
		},
	}
	handler := server.Handler()
	list := perform(handler, http.MethodGet, "/api/groups", true)
	get := perform(handler, http.MethodGet, "/api/groups/application-a", true)
	missing := perform(handler, http.MethodGet, "/api/groups/missing", true)
	unauthorized := perform(handler, http.MethodGet, "/api/groups", false)
	releaseList := perform(handler, http.MethodGet, "/api/groups/application-a/releases", true)
	releaseDetail := perform(handler, http.MethodGet, "/api/groups/application-a/releases/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true)
	invalidCaddyfile := performMultipartWith(handler, "invalid-key-00001", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "example.test { totally_unknown_directive }\n")
	pointersAfterInvalid := perform(handler, http.MethodGet, "/api/groups/application-a", true)
	staleRevision := performMultipartWith(handler, "stale-key-000001", "", "example.test { respond 202 }\n")
	pointersAfterStale := perform(handler, http.MethodGet, "/api/groups/application-a", true)
	publish := performMultipart(handler)
	publishRetry := performMultipartWith(handler, "publish-key-00001", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "example.test { respond 200 }\n")
	idempotencyConflict := performMultipartWith(handler, "publish-key-00001", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "example.test { respond 201 }\n")

	var groupList struct {
		Items     []groupView `json:"items"`
		RequestID string      `json:"requestId"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &groupList); err != nil {
		panic(err)
	}
	var group struct {
		groupView
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &group); err != nil {
		panic(err)
	}
	var missingBody struct {
		Code      string `json:"code"`
		Status    int    `json:"status"`
		Detail    string `json:"detail"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(missing.Body.Bytes(), &missingBody); err != nil {
		panic(err)
	}
	var releaseListBody struct {
		Items     []map[string]any `json:"items"`
		RequestID string           `json:"requestId"`
	}
	var releaseDetailBody map[string]any
	if err := json.Unmarshal(releaseDetail.Body.Bytes(), &releaseDetailBody); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(releaseList.Body.Bytes(), &releaseListBody); err != nil {
		panic(err)
	}
	var publishBody map[string]any
	if err := json.Unmarshal(publish.Body.Bytes(), &publishBody); err != nil {
		panic(err)
	}
	var publishRetryBody, idempotencyConflictBody, invalidCaddyfileBody, staleRevisionBody map[string]any
	for response, destination := range map[*httptest.ResponseRecorder]*map[string]any{
		publishRetry: &publishRetryBody, idempotencyConflict: &idempotencyConflictBody,
		invalidCaddyfile: &invalidCaddyfileBody, staleRevision: &staleRevisionBody,
	} {
		if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
			panic(err)
		}
	}
	var pointersAfterInvalidBody, pointersAfterStaleBody struct {
		CurrentRevision *string `json:"currentRevision"`
	}
	if err := json.Unmarshal(pointersAfterInvalid.Body.Bytes(), &pointersAfterInvalidBody); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(pointersAfterStale.Body.Bytes(), &pointersAfterStaleBody); err != nil {
		panic(err)
	}
	releasePathsHidden := len(releaseListBody.Items) == 1
	if releasePathsHidden {
		_, caddyfilePath := releaseListBody.Items[0]["caddyfilePath"]
		_, artifactPath := releaseListBody.Items[0]["artifactPath"]
		releasePathsHidden = !caddyfilePath && !artifactPath
	}
	response := report{
		ListStatus: list.Code, ListRequestID: groupList.RequestID != "", Groups: groupList.Items,
		GetStatus: get.Code, GetRequestID: group.RequestID != "", GetGroup: group.groupView,
		MissingStatus:      missing.Code,
		MissingProblem:     problemView{Code: missingBody.Code, Status: missingBody.Status, RequestID: missingBody.RequestID != "", NoStoreDetail: missingBody.Detail != "The requested group does not exist."},
		UnauthorizedStatus: unauthorized.Code,
		ReleaseListStatus:  releaseList.Code, ReleaseListRequestID: releaseListBody.RequestID != "",
		Releases: releaseListBody.Items, ReleasePathsHidden: releasePathsHidden,
		ReleaseDetailStatus: releaseDetail.Code,
		ReleaseDetailSafe:   releaseDetailBody["caddyfile"] == "example.test { respond 200 }\n" && releaseDetailBody["id"] == "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" && releaseDetailBody["requestId"] != "" && releaseDetailBody["caddyfilePath"] == nil && releaseDetailBody["artifactPath"] == nil,
		PublishStatus:       publish.Code,
		Publish:             publishBody,
		PublishRetryStatus:  publishRetry.Code, PublishRetry: publishRetryBody,
		IdempotencyConflictStatus: idempotencyConflict.Code, IdempotencyConflictCode: stringField(idempotencyConflictBody, "code"),
		InvalidCaddyfileStatus: invalidCaddyfile.Code, InvalidCaddyfileCode: stringField(invalidCaddyfileBody, "code"),
		PointersAfterInvalid: pointersAfterInvalidBody.CurrentRevision,
		StaleRevisionStatus:  staleRevision.Code, StaleRevisionCode: stringField(staleRevisionBody, "code"),
		PointersAfterStale: pointersAfterStaleBody.CurrentRevision,
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		panic(err)
	}
}

func perform(handler http.Handler, method, path string, authorized bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if authorized {
		request.Header.Set("Authorization", "Bearer fixture-management-token")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func performMultipart(handler http.Handler) *httptest.ResponseRecorder {
	return performMultipartWith(handler, "publish-key-00001", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "example.test { respond 200 }\n")
}

func performMultipartWith(handler http.Handler, idempotencyKey, expectedCurrentRevision, caddyfile string) *httptest.ResponseRecorder {
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	metadata, err := json.Marshal(map[string]any{"idempotencyKey": idempotencyKey, "expectedCurrentRevision": nullableString(expectedCurrentRevision)})
	if err != nil {
		panic(err)
	}
	if err := writer.WriteField("metadata", string(metadata)); err != nil {
		panic(err)
	}
	if err := writer.WriteField("caddyfile", caddyfile); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/groups/application-a/releases", strings.NewReader(body.String()))
	request.Header.Set("Authorization", "Bearer fixture-management-token")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func stringField(value map[string]any, key string) string {
	field, _ := value[key].(string)
	return field
}
