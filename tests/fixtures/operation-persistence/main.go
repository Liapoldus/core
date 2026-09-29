package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

const instanceID = "forms"

type fixtureApplier struct {
	revision string
	raw      []byte
}

func (a *fixtureApplier) ApplyConfiguration(_ context.Context, _ string, revision string, raw []byte) error {
	a.revision = revision
	a.raw = append(a.raw[:0], raw...)
	return nil
}

type report struct {
	CreateStatus       int            `json:"createStatus"`
	OperationID        string         `json:"operationId"`
	ReadStatus         int            `json:"readStatus"`
	Operation          map[string]any `json:"operation"`
	UnknownStatus      int            `json:"unknownStatus"`
	UnknownCode        string         `json:"unknownCode"`
	ResultSecretAbsent bool           `json:"resultSecretAbsent"`
	StoredPayloadsNull bool           `json:"storedPayloadsNull"`
	AppliedRevision    string         `json:"appliedRevision"`
	AppliedRaw         string         `json:"appliedRaw"`
}

func main() {
	path := os.Args[1]
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
	database := openDatabase(path)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	if err != nil {
		panic(err)
	}
	seedPluginRevisions(database, instanceID)
	server := &api.Server{
		Token: "fixture-management-token", Management: management, AuditWords: auditWords, Errors: errorCatalog,
		Operations: application.OperationService{Store: operationStore},
	}
	configurationStore, err := storage.NewSQLitePluginConfigurationStore(database)
	if err != nil {
		panic(err)
	}
	pluginConfiguration, err := config.LoadPluginConfiguration()
	if err != nil {
		panic(err)
	}
	applier := &fixtureApplier{}
	pluginConfigurations := &application.PluginConfigurationService{
		Store: configurationStore, Applier: applier,
		PayloadVersion: pluginConfiguration.SchemaVersion,
		RevisionStates: application.PluginConfigurationRevisionStates{
			Active: pluginConfiguration.Slots.Active, Candidate: pluginConfiguration.Slots.Staging,
		},
	}
	handler := api.WithPluginConfigurations(server.Handler(), pluginConfigurations)
	created := performWithHeaders(handler, http.MethodPost,
		management.Paths.Plugins+"/"+instanceID+"/"+management.Paths.PluginRollbackSuffix, true,
		map[string]string{management.Headers.IfMatch: `"2"`, management.Idempotency.Key: "fixture-operation-key"})
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
	server = &api.Server{Token: "fixture-management-token", Management: management, AuditWords: auditWords, Errors: errorCatalog, Operations: application.OperationService{Store: operationStore}}
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
		AppliedRevision:    applier.revision,
		AppliedRaw:         string(applier.raw),
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

func seedPluginRevisions(database *sql.DB, id string) {
	ctx := context.Background()
	if _, err := database.ExecContext(ctx,
		`INSERT INTO plugin_instances (id, mode, manifest_json, state) VALUES (?, ?, ?, ?)`,
		id, "local", []byte(`{"name":"fixture"}`), "configured"); err != nil {
		panic(err)
	}
	for _, generation := range []struct {
		number int64
		slot   string
		raw    string
	}{{1, "previous", `{"generation":1}`}, {2, "active", `{"generation":2}`}} {
		digest := sha256.Sum256([]byte(generation.raw))
		if _, err := database.ExecContext(ctx,
			`INSERT INTO plugin_config_generations (instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
			 VALUES (?, ?, ?, ?, ?, 1, ?)`,
			id, generation.number, generation.slot, []byte(generation.raw),
			hex.EncodeToString(digest[:]), time.Now().UTC().Format(time.RFC3339)); err != nil {
			panic(err)
		}
	}
}

func perform(handler http.Handler, method, path string, authorized bool) *httptest.ResponseRecorder {
	return performWithHeaders(handler, method, path, authorized, nil)
}

func performWithHeaders(handler http.Handler, method, path string, authorized bool, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if authorized {
		request.Header.Set("Authorization", "Bearer fixture-management-token")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
