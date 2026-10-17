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

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
	"github.com/Liapoldus/core/v3/internal/presentation/api"
)

const instanceID = "forms"

type fixtureApplier struct {
	revision string
	raw      []byte
}

// This fixture isolates activation; candidate validation succeeds independently.
func (*fixtureApplier) ValidateTrafficRolloutConfiguration(context.Context, string, []byte) error {
	return nil
}

func (a *fixtureApplier) ApplyConfiguration(_ context.Context, _ string, revision string, raw []byte) error {
	a.revision = revision
	a.raw = append(a.raw[:0], raw...)
	return nil
}

type report struct {
	CreateStatus       int              `json:"createStatus"`
	OperationID        string           `json:"operationId"`
	ReadStatus         int              `json:"readStatus"`
	Operation          map[string]any   `json:"operation"`
	UnknownStatus      int              `json:"unknownStatus"`
	UnknownCode        string           `json:"unknownCode"`
	ResolveStatus      int              `json:"resolveStatus"`
	ResolvedOperations []map[string]any `json:"resolvedOperations"`
	ResolvedKeyAbsent  bool             `json:"resolvedKeyAbsent"`
	ResultSecretAbsent bool             `json:"resultSecretAbsent"`
	StoredPayloadsNull bool             `json:"storedPayloadsNull"`
	AppliedRevision    string           `json:"appliedRevision"`
	AppliedRaw         string           `json:"appliedRaw"`
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
	seedServiceRevisions(database, instanceID)
	server := &api.Server{
		Token: "fixture-management-token", Management: management, AuditWords: auditWords, Errors: errorCatalog,
		Operations: application.OperationService{Store: operationStore},
	}
	configurationStore, err := storage.NewSQLiteServiceConfigurationStore(database)
	if err != nil {
		panic(err)
	}
	serviceConfiguration, err := config.LoadServiceConfiguration()
	if err != nil {
		panic(err)
	}
	applier := &fixtureApplier{}
	serviceConfigurations := &application.ServiceConfigurationService{
		Store: configurationStore, Applier: applier,
		PayloadVersion: serviceConfiguration.SchemaVersion,
		RevisionStates: application.ServiceConfigurationRevisionStates{
			Active: serviceConfiguration.Slots.Active, Candidate: serviceConfiguration.Slots.Staging,
		},
	}
	handler := api.WithServiceConfigurations(server.Handler(), serviceConfigurations)
	created := performWithHeaders(handler, http.MethodPost,
		management.Paths.Services+"/"+instanceID+"/"+management.Paths.ServiceRollbackSuffix, true,
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
	resolveBody, err := json.Marshal(map[string]any{
		"scope": management.Paths.Services + "/" + instanceID + "/" + management.Paths.ServiceRollbackSuffix,
		"keys":  []string{"fixture-operation-key", "unknown-key"},
	})
	if err != nil {
		panic(err)
	}
	resolveRequest := httptest.NewRequest(http.MethodPost, management.Paths.OperationResolve, strings.NewReader(string(resolveBody)))
	resolveRequest.Header.Set("Authorization", "Bearer fixture-management-token")
	resolveRequest.Header.Set("Content-Type", "application/json")
	resolveResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(resolveResponse, resolveRequest)
	var operation map[string]any
	if err := json.Unmarshal(read.Body.Bytes(), &operation); err != nil {
		panic(err)
	}
	var unknownBody map[string]any
	if err := json.Unmarshal(unknown.Body.Bytes(), &unknownBody); err != nil {
		panic(err)
	}
	var resolvedBody struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resolveResponse.Body.Bytes(), &resolvedBody); err != nil {
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
		ResolveStatus:      resolveResponse.Code,
		ResolvedOperations: resolvedBody.Items,
		ResolvedKeyAbsent:  !strings.Contains(resolveResponse.Body.String(), "fixture-operation-key"),
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

func seedServiceRevisions(database *sql.DB, id string) {
	ctx := context.Background()
	if _, err := database.ExecContext(ctx,
		`INSERT INTO service_instances (id, manifest_json, state) VALUES (?, ?, ?)`,
		id, []byte(`{"name":"fixture"}`), "configured"); err != nil {
		panic(err)
	}
	for _, generation := range []struct {
		number int64
		slot   string
		raw    string
	}{{1, "previous", `{"generation":1}`}, {2, "active", `{"generation":2}`}} {
		digest := sha256.Sum256([]byte(generation.raw))
		if _, err := database.ExecContext(ctx,
			`INSERT INTO service_config_generations (instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
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
