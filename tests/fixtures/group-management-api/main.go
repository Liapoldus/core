package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
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
	ListStatus           int              `json:"listStatus"`
	ListRequestID        bool             `json:"listRequestID"`
	Groups               []groupView      `json:"groups"`
	GetStatus            int              `json:"getStatus"`
	GetRequestID         bool             `json:"getRequestID"`
	GetGroup             groupView        `json:"getGroup"`
	MissingStatus        int              `json:"missingStatus"`
	MissingProblem       problemView      `json:"missingProblem"`
	UnauthorizedStatus   int              `json:"unauthorizedStatus"`
	ReleaseListStatus    int              `json:"releaseListStatus"`
	ReleaseListRequestID bool             `json:"releaseListRequestID"`
	Releases             []map[string]any `json:"releases"`
	ReleasePathsHidden   bool             `json:"releasePathsHidden"`
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
	if _, err := store.CreateApplicationGroup(ctx, "application-a"); err != nil {
		panic(err)
	}
	if _, err := store.CreateRevision(ctx, models.GroupRevision{
		ID: "revision-a", GroupID: "application-a", CaddyfileDigest: "digest-a", CaddyfilePath: "revision-a.caddyfile",
	}); err != nil {
		panic(err)
	}
	if _, err := store.AdvanceCurrent(ctx, "application-a", "revision-a", nil); err != nil {
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
		GroupService: application.GroupService{Store: store},
	}
	handler := server.Handler()
	list := perform(handler, http.MethodGet, "/api/groups", true)
	get := perform(handler, http.MethodGet, "/api/groups/application-a", true)
	missing := perform(handler, http.MethodGet, "/api/groups/missing", true)
	unauthorized := perform(handler, http.MethodGet, "/api/groups", false)
	releaseList := perform(handler, http.MethodGet, "/api/groups/application-a/releases", true)

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
	if err := json.Unmarshal(releaseList.Body.Bytes(), &releaseListBody); err != nil {
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
