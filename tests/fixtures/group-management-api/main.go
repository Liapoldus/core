package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/caddy"
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
	ListStatus                 int              `json:"listStatus"`
	ListRequestID              bool             `json:"listRequestID"`
	Groups                     []groupView      `json:"groups"`
	GetStatus                  int              `json:"getStatus"`
	GetRequestID               bool             `json:"getRequestID"`
	GetGroup                   groupView        `json:"getGroup"`
	MissingStatus              int              `json:"missingStatus"`
	MissingProblem             problemView      `json:"missingProblem"`
	UnauthorizedStatus         int              `json:"unauthorizedStatus"`
	ReleaseListStatus          int              `json:"releaseListStatus"`
	ReleaseListRequestID       bool             `json:"releaseListRequestID"`
	Releases                   []map[string]any `json:"releases"`
	ReleasePathsHidden         bool             `json:"releasePathsHidden"`
	ReleaseDetailStatus        int              `json:"releaseDetailStatus"`
	ReleaseDetailSafe          bool             `json:"releaseDetailSafe"`
	PublishStatus              int              `json:"publishStatus"`
	Publish                    map[string]any   `json:"publish"`
	PublishRetryStatus         int              `json:"publishRetryStatus"`
	PublishRetry               map[string]any   `json:"publishRetry"`
	OperationAfterReopenStatus int              `json:"operationAfterReopenStatus"`
	OperationAfterReopen       map[string]any   `json:"operationAfterReopen"`
	GroupAfterReopenStatus     int              `json:"groupAfterReopenStatus"`
	CurrentAfterReopen         *string          `json:"currentAfterReopen"`
	IdempotencyConflictStatus  int              `json:"idempotencyConflictStatus"`
	IdempotencyConflictCode    string           `json:"idempotencyConflictCode"`
	InvalidCaddyfileStatus     int              `json:"invalidCaddyfileStatus"`
	InvalidCaddyfileCode       string           `json:"invalidCaddyfileCode"`
	PointersAfterInvalid       *string          `json:"pointersAfterInvalid"`
	StaleRevisionStatus        int              `json:"staleRevisionStatus"`
	StaleRevisionCode          string           `json:"staleRevisionCode"`
	PointersAfterStale         *string          `json:"pointersAfterStale"`
	UnsafeArtifactStatus       int              `json:"unsafeArtifactStatus"`
	UnsafeArtifactCode         string           `json:"unsafeArtifactCode"`
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
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", GroupID: "application-a", CaddyfileDigest: "a1ec39a1a96fd53b4e5b58e0734941379bb4e7c19130f04d2c577262b6f8d95c", CaddyfilePath: "revision-a.caddyfile",
	}); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(os.Args[1]), "revision-a.caddyfile"), []byte("example.test {\n  respond 200\n}\n"), 0o600); err != nil {
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
	auditWords, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	operationStore, err := storage.NewSQLiteOperationStore(database)
	if err != nil {
		panic(err)
	}
	releaseStore, err := storage.NewSQLiteGroupReleaseStore(database)
	if err != nil {
		panic(err)
	}
	releasePolicy, err := config.LoadGroupRelease()
	if err != nil {
		panic(err)
	}
	releaseArtifacts, err := artifacts.NewGroupReleaseArtifacts(filepath.Dir(os.Args[1]))
	if err != nil {
		panic(err)
	}
	releaseService := &application.GroupReleaseService{
		Store: store, Releases: releaseStore,
		ContentReader: artifacts.GroupRevisionReader{Root: filepath.Dir(os.Args[1])},
		Artifacts:     releaseArtifacts, Activator: fixtureActivator{}, Policy: releasePolicy,
	}
	server := &api.Server{
		Token: "fixture-management-token", Management: management, Errors: errorCatalog, AuditWords: auditWords,
		Operations:    application.OperationService{Store: operationStore},
		GroupReleases: releaseService, GroupReleasePolicy: releasePolicy,
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
	invalidCaddyfile := performMultipartWith(handler, "invalid-key-00001", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "example.test {\n  totally_unknown_directive\n}\n")
	pointersAfterInvalid := perform(handler, http.MethodGet, "/api/groups/application-a", true)
	staleRevision := performMultipartWith(handler, "stale-key-000001", "", "example.test {\n  respond 202\n}\n")
	pointersAfterStale := perform(handler, http.MethodGet, "/api/groups/application-a", true)
	publish := performMultipart(handler)
	publishRetry := performMultipartWith(handler, "publish-key-00001", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "example.test {\n  respond 200\n}\n")
	idempotencyConflict := performMultipartWith(handler, "publish-key-00001", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "example.test {\n  respond 201\n}\n")
	unsafeArtifact := performMultipartWithArtifact(handler, "unsafe-key-00001", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "example.test {\n  respond 200\n}\n", archive("frontends/ui/../escape.txt"))

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
	var publishRetryBody, idempotencyConflictBody, invalidCaddyfileBody, staleRevisionBody, unsafeArtifactBody map[string]any
	for response, destination := range map[*httptest.ResponseRecorder]*map[string]any{
		publishRetry: &publishRetryBody, idempotencyConflict: &idempotencyConflictBody,
		invalidCaddyfile: &invalidCaddyfileBody, staleRevision: &staleRevisionBody, unsafeArtifact: &unsafeArtifactBody,
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
	operationID, _ := publishBody[management.JSON.OperationID].(string)
	if operationID != "" {
		waitForOperation(operationStore, operationID)
	}
	if err := database.Close(); err != nil {
		panic(err)
	}
	reopenedDatabase, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqliteContract.Driver, ParentDirectoryMode: sqliteContract.ParentDirectoryMode,
		DatabaseFileMode: sqliteContract.DatabaseFileMode, MaxOpenConnections: sqliteContract.MaxOpenConnections,
		MaxIdleConnections: sqliteContract.MaxIdleConnections, SchemaVersion: sqliteContract.SchemaVersion,
		HasMigrationTableQuery: sqliteContract.HasMigrationTableQuery, MigrationVersionQuery: sqliteContract.MigrationVersionQuery,
		SchemaVersionError: sqliteContract.SchemaVersionError, Pragmas: sqliteContract.Pragmas,
	}, sqliteContract.Schema)
	if err != nil {
		panic(err)
	}
	defer reopenedDatabase.Close()
	reopenedGroupStore, err := storage.NewSQLiteGroupStore(reopenedDatabase)
	if err != nil {
		panic(err)
	}
	reopenedOperationStore, err := storage.NewSQLiteOperationStore(reopenedDatabase)
	if err != nil {
		panic(err)
	}
	reopenedServer := &api.Server{
		Token: "fixture-management-token", Management: management, Errors: errorCatalog, AuditWords: auditWords,
		GroupService: application.GroupService{
			Store: reopenedGroupStore, ContentReader: artifacts.GroupRevisionReader{Root: filepath.Dir(os.Args[1])},
		},
		Operations: application.OperationService{Store: reopenedOperationStore},
	}
	operationAfterReopen := perform(reopenedServer.Handler(), http.MethodGet, management.Paths.Operations+"/"+operationID, true)
	groupAfterReopen := perform(reopenedServer.Handler(), http.MethodGet, "/api/groups/application-a", true)
	var operationAfterReopenBody map[string]any
	if err := json.Unmarshal(operationAfterReopen.Body.Bytes(), &operationAfterReopenBody); err != nil {
		panic(err)
	}
	var groupAfterReopenBody struct {
		CurrentRevision *string `json:"currentRevision"`
	}
	if err := json.Unmarshal(groupAfterReopen.Body.Bytes(), &groupAfterReopenBody); err != nil {
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
		ReleaseDetailSafe:   releaseDetailBody["caddyfile"] == "example.test {\n  respond 200\n}\n" && releaseDetailBody["id"] == "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" && releaseDetailBody["requestId"] != "" && releaseDetailBody["caddyfilePath"] == nil && releaseDetailBody["artifactPath"] == nil,
		PublishStatus:       publish.Code,
		Publish:             publishBody,
		PublishRetryStatus:  publishRetry.Code, PublishRetry: publishRetryBody,
		OperationAfterReopenStatus: operationAfterReopen.Code, OperationAfterReopen: operationAfterReopenBody,
		GroupAfterReopenStatus: groupAfterReopen.Code, CurrentAfterReopen: groupAfterReopenBody.CurrentRevision,
		IdempotencyConflictStatus: idempotencyConflict.Code, IdempotencyConflictCode: stringField(idempotencyConflictBody, "code"),
		InvalidCaddyfileStatus: invalidCaddyfile.Code, InvalidCaddyfileCode: stringField(invalidCaddyfileBody, "code"),
		PointersAfterInvalid: pointersAfterInvalidBody.CurrentRevision,
		StaleRevisionStatus:  staleRevision.Code, StaleRevisionCode: stringField(staleRevisionBody, "code"),
		PointersAfterStale:   pointersAfterStaleBody.CurrentRevision,
		UnsafeArtifactStatus: unsafeArtifact.Code, UnsafeArtifactCode: stringField(unsafeArtifactBody, "code"),
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
	return performMultipartWith(handler, "publish-key-00001", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "example.test {\n  respond 200\n}\n")
}

func performMultipartWith(handler http.Handler, idempotencyKey, expectedCurrentRevision, caddyfile string) *httptest.ResponseRecorder {
	return performMultipartWithArtifact(handler, idempotencyKey, expectedCurrentRevision, caddyfile, nil)
}

func performMultipartWithArtifact(handler http.Handler, idempotencyKey, expectedCurrentRevision, caddyfile string, artifact []byte) *httptest.ResponseRecorder {
	var body strings.Builder
	writer := multipart.NewWriter(&body)
	metadata, err := json.Marshal(map[string]any{"idempotencyKey": idempotencyKey, "expectedCurrentRevision": nullableString(expectedCurrentRevision)})
	if err != nil {
		panic(err)
	}
	metadataPart, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {"form-data; name=\"metadata\""},
		"Content-Type":        {"application/json"},
	})
	if err != nil {
		panic(err)
	}
	if _, err := metadataPart.Write(metadata); err != nil {
		panic(err)
	}
	caddyfilePart, err := writer.CreatePart(map[string][]string{
		"Content-Disposition": {"form-data; name=\"caddyfile\"; filename=\"Caddyfile\""},
		"Content-Type":        {"text/plain; charset=utf-8"},
	})
	if err != nil {
		panic(err)
	}
	if _, err := caddyfilePart.Write([]byte(caddyfile)); err != nil {
		panic(err)
	}
	if artifact != nil {
		artifactPart, err := writer.CreatePart(map[string][]string{
			"Content-Disposition": {"form-data; name=\"artifact\"; filename=\"site.tar.gz\""},
			"Content-Type":        {"application/gzip"},
		})
		if err != nil {
			panic(err)
		}
		if _, err := artifactPart.Write(artifact); err != nil {
			panic(err)
		}
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

func archive(path string) []byte {
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	writer := tar.NewWriter(compressor)
	contents := []byte("content")
	if err := writer.WriteHeader(&tar.Header{Name: path, Mode: 0o600, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
		panic(err)
	}
	if _, err := writer.Write(contents); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	if err := compressor.Close(); err != nil {
		panic(err)
	}
	return compressed.Bytes()
}

type fixtureActivator struct{}

func (fixtureActivator) Validate(_ context.Context, source []byte) error {
	_, _, err := caddy.AdaptCaddyfile(source)
	return err
}

func (fixtureActivator) Activate(context.Context, []byte) error { return nil }

func waitForOperation(store *storage.SQLiteOperationStore, id string) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		operation, err := store.Get(context.Background(), id)
		if err != nil {
			panic(err)
		}
		if operation.State == "succeeded" || operation.State == "failed" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	panic("operation did not finish before fixture deadline")
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
