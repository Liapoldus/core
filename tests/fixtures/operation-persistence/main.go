package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type report struct {
	CreateStatus       int            `json:"createStatus"`
	OperationID        string         `json:"operationId"`
	ReadStatus         int            `json:"readStatus"`
	Operation          map[string]any `json:"operation"`
	UnknownStatus      int            `json:"unknownStatus"`
	UnknownCode        string         `json:"unknownCode"`
	ResultSecretAbsent bool           `json:"resultSecretAbsent"`
	StoredPayloadsNull bool           `json:"storedPayloadsNull"`
}

func main() {
	path := os.Args[1]
	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	errorCatalog, err := config.LoadErrorCatalog()
	if err != nil {
		panic(err)
	}
	database := openDatabase(path)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	if err != nil {
		panic(err)
	}
	server := &api.Server{
		Token: "fixture-management-token", Management: management, Errors: errorCatalog,
		Operations: application.OperationService{Store: operationStore},
		RestartPlugin: func(context.Context, string) (models.Operation, error) {
			return models.Operation{
				ID: "operation-restart-1", Kind: "plugin-restart", State: "running", CreatedAt: time.Now().UTC(),
			}, nil
		},
	}
	created := perform(server.Handler(), http.MethodPost, management.Paths.Plugins+"/forms/"+management.Paths.Restart, true)
	if created.Code != http.StatusAccepted {
		panic(created.Body.String())
	}
	var reference map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &reference); err != nil {
		panic(err)
	}
	operationID, _ := reference[management.JSON.OperationID].(string)
	if err := database.Close(); err != nil {
		panic(err)
	}
	database = openDatabase(path)
	defer database.Close()
	operationStore, err = storage.NewSQLiteOperationStore(database)
	if err != nil {
		panic(err)
	}
	server = &api.Server{Token: "fixture-management-token", Management: management, Errors: errorCatalog, Operations: application.OperationService{Store: operationStore}}
	read := perform(server.Handler(), http.MethodGet, management.Paths.Operations+"/"+operationID, true)
	unknown := perform(server.Handler(), http.MethodGet, management.Paths.Operations+"/unknown-operation", true)
	var operation map[string]any
	if err := json.Unmarshal(read.Body.Bytes(), &operation); err != nil {
		panic(err)
	}
	var unknownBody map[string]any
	if err := json.Unmarshal(unknown.Body.Bytes(), &unknownBody); err != nil {
		panic(err)
	}
	_, leaked := operation[management.JSON.Result]
	var resultJSON, problemJSON sql.NullString
	if err := database.QueryRowContext(context.Background(), "SELECT result_json, problem_json FROM operations WHERE id = ?", operationID).Scan(&resultJSON, &problemJSON); err != nil {
		panic(err)
	}
	result := report{
		CreateStatus: created.Code, OperationID: operationID,
		ReadStatus: read.Code, Operation: operation,
		UnknownStatus:      unknown.Code,
		ResultSecretAbsent: !leaked && !strings.Contains(read.Body.String(), "must-not-be-persisted-or-returned"),
		StoredPayloadsNull: !resultJSON.Valid && !problemJSON.Valid,
	}
	if code, ok := unknownBody["code"].(string); ok {
		result.UnknownCode = code
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}

func openDatabase(path string) *sql.DB {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(context.Background(), path, storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	if err != nil {
		panic(err)
	}
	return database
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
