package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

func main() {
	ctx := context.Background()
	contract, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	check(err)
	defer database.Close()
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances (id, mode, settings_json, manifest_json, state, revision) VALUES (?, ?, ?, ?, ?, ?)`,
		"fixture", "remote", []byte(`{"mode":"fixture"}`), []byte(`{"name":"fixture"}`), "configured", 1)
	check(err)
	store, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	management, err := config.LoadManagement()
	check(err)
	service := &application.PluginConfigurationService{Store: store, Unavailable: management.Codes.ManagementUnavailable}
	server := api.WithPluginConfigurations((&api.Server{Token: "fixture-token", Management: management}).Handler(), service)
	get := perform(server, "/api/plugins/fixture/settings")
	missing := perform(server, "/api/plugins/missing/settings")
	var body any
	check(json.Unmarshal(get.Body.Bytes(), &body))
	var missingBody struct {
		Code string `json:"code"`
	}
	check(json.Unmarshal(missing.Body.Bytes(), &missingBody))
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"status": get.Code, "etag": get.Header().Get("ETag"), "settings": body,
		"missingStatus": missing.Code, "missingCode": missingBody.Code,
	}))
}

func perform(handler http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer fixture-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
