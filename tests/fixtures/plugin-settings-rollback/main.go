package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type applyCall struct {
	Revision string `json:"revision"`
	Config   string `json:"config"`
}

type fixtureApplier struct{ calls []applyCall }

func (applier *fixtureApplier) ApplyConfiguration(_ context.Context, _ string, revision string, configuration []byte) error {
	applier.calls = append(applier.calls, applyCall{Revision: revision, Config: string(configuration)})
	if bytes.Contains(configuration, []byte(`"reject"`)) {
		return errors.New("configuration rejected")
	}
	return nil
}

type observation struct {
	Status      int            `json:"status"`
	ContentType string         `json:"contentType"`
	Location    string         `json:"location"`
	Body        map[string]any `json:"body"`
}

func main() {
	ctx := context.Background()
	management, err := config.LoadManagement()
	check(err)
	pluginConfigurationWords, err := config.LoadPluginConfiguration()
	check(err)
	auditWords, err := config.LoadAudit()
	check(err)
	errorCatalog, err := config.LoadErrorCatalog()
	check(err)
	sqlite, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqlite.Driver, ParentDirectoryMode: sqlite.ParentDirectoryMode,
		DatabaseFileMode: sqlite.DatabaseFileMode, MaxOpenConnections: sqlite.MaxOpenConnections,
		MaxIdleConnections: sqlite.MaxIdleConnections, SchemaVersion: sqlite.SchemaVersion,
		HasMigrationTableQuery: sqlite.HasMigrationTableQuery, MigrationVersionQuery: sqlite.MigrationVersionQuery,
		SchemaVersionError: sqlite.SchemaVersionError, Pragmas: sqlite.Pragmas,
	}, sqlite.Schema)
	check(err)
	defer database.Close()
	seedInstance(ctx, database, "fixture", []seededRevision{
		{1, "previous", "{\n  \"origin\" : \"first\"\n}\n"},
		{2, "active", "{ \"origin\" : \"second\" }\n"},
	})
	seedInstance(ctx, database, "single", []seededRevision{
		{1, "active", `{"origin":"only"}`},
	})
	seedInstance(ctx, database, "rejecting", []seededRevision{
		{1, "previous", `{"reject":true}`},
		{2, "active", `{"origin":"current"}`},
	})
	configurationStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	applier := &fixtureApplier{}
	service := &application.PluginConfigurationService{
		Store: configurationStore, Applier: applier,
		PayloadVersion: pluginConfigurationWords.SchemaVersion,
		RevisionStates: application.PluginConfigurationRevisionStates{
			Active: pluginConfigurationWords.Slots.Active, Candidate: pluginConfigurationWords.Slots.Staging,
		},
	}
	server := &api.Server{
		Token: "fixture-token", Management: management, AuditWords: auditWords, Errors: errorCatalog,
		Operations: application.OperationService{Store: operationStore},
	}
	handler := api.WithPluginConfigurations(server.Handler(), service)
	rollback := func(id, ifMatch, key string, body []byte) observation {
		path := management.Paths.Plugins + "/" + id + "/" + management.Paths.PluginRollbackSuffix
		return observe(perform(handler, http.MethodPost, path, body, ifMatch, key, management), management)
	}
	accepted := rollback("fixture", `"2"`, "rollback-key-0001", []byte(`{"origin":"ignored request body"}`))
	replay := rollback("fixture", `"2"`, "rollback-key-0001", nil)
	conflicting := rollback("fixture", `"2"`, "rollback-key-0002", nil)
	missingIfMatch := rollback("fixture", "", "rollback-key-0003", nil)
	missingKey := rollback("fixture", `"2"`, "", nil)
	unknown := rollback("missing", `"2"`, "rollback-key-0004", nil)
	withoutPrevious := rollback("single", `"1"`, "rollback-key-0005", nil)
	rejected := rollback("rejecting", `"2"`, "rollback-key-0006", nil)
	unauthorized := observe(performUnauthorized(handler, http.MethodPost,
		management.Paths.Plugins+"/fixture/"+management.Paths.PluginRollbackSuffix, management), management)

	var activeRaw []byte
	var activeDigest string
	check(database.QueryRowContext(ctx,
		`SELECT raw_json, sha256 FROM plugin_config_generations WHERE instance_id = ? AND slot = ?`,
		"fixture", pluginConfigurationWords.Slots.Active).Scan(&activeRaw, &activeDigest))
	rejectedState, rejectedErrorCode := readOperation(ctx, database)
	actor, actions := readAudit(ctx, database)
	writeReport(map[string]any{
		"accepted": accepted, "replay": replay, "conflicting": conflicting,
		"missingIfMatch": missingIfMatch, "missingKey": missingKey,
		"unknown": unknown, "withoutPrevious": withoutPrevious, "rejected": rejected,
		"unauthorized": unauthorized, "rejectedState": rejectedState, "rejectedErrorCode": rejectedErrorCode,
		"applyCalls": applier.calls, "activeRaw": string(activeRaw), "activeDigest": activeDigest,
		"auditedActor": actor, "auditActions": actions,
	})
}

type seededRevision struct {
	number int64
	slot   string
	raw    string
}

func seedInstance(ctx context.Context, database *sql.DB, id string, revisions []seededRevision) {
	if _, err := database.ExecContext(ctx,
		`INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)`,
		id, []byte(`{"name":"fixture"}`), "configured"); err != nil {
		panic(err)
	}
	for _, revision := range revisions {
		digest := sha256.Sum256([]byte(revision.raw))
		if _, err := database.ExecContext(ctx,
			`INSERT INTO plugin_config_generations (instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
			 VALUES (?, ?, ?, ?, ?, 1, ?)`,
			id, revision.number, revision.slot, []byte(revision.raw),
			hex.EncodeToString(digest[:]), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			panic(err)
		}
	}
}

func observe(response *httptest.ResponseRecorder, management config.ManagementWords) observation {
	result := observation{
		Status:      response.Code,
		ContentType: response.Header().Get(management.Headers.ContentType),
		Location:    response.Header().Get(management.Headers.Location),
	}
	if len(response.Body.Bytes()) > 0 {
		check(json.Unmarshal(response.Body.Bytes(), &result.Body))
	}
	return result
}

func perform(handler http.Handler, method, path string, body []byte, ifMatch, idempotencyKey string, management config.ManagementWords) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture-token")
	if ifMatch != "" {
		request.Header.Set(management.Headers.IfMatch, ifMatch)
	}
	if idempotencyKey != "" {
		request.Header.Set(management.Idempotency.Key, idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func performUnauthorized(handler http.Handler, method, path string, management config.ManagementWords) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func readOperation(ctx context.Context, database *sql.DB) (string, string) {
	var state, problem []byte
	if err := database.QueryRowContext(ctx,
		`SELECT state, problem_json FROM operations WHERE kind = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		"plugin-settings-rollback").Scan(&state, &problem); err != nil {
		panic(err)
	}
	var decoded struct {
		Code string `json:"errorCode"`
	}
	if len(problem) > 0 {
		check(json.Unmarshal(problem, &decoded))
	}
	return string(state), decoded.Code
}

func readAudit(ctx context.Context, database *sql.DB) (string, []string) {
	rows, err := database.QueryContext(ctx, `SELECT actor, action FROM audit_events ORDER BY sequence`)
	check(err)
	defer rows.Close()
	actor := ""
	actions := make([]string, 0)
	for rows.Next() {
		var currentActor, action string
		check(rows.Scan(&currentActor, &action))
		if actor == "" {
			actor = currentActor
		}
		actions = append(actions, action)
	}
	check(rows.Err())
	return actor, actions
}

func writeReport(report map[string]any) {
	check(json.NewEncoder(os.Stdout).Encode(report))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
