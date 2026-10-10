package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
	_ "modernc.org/sqlite"
)

const (
	activeRaw   = `{ "version" : 2 }`
	previousRaw = `{"version":1}`
	stagingRaw  = `{ "version" : 3 }`
	timestamp   = "2026-09-29T00:00:00Z"
)

func main() {
	ctx := context.Background()
	if len(os.Args) != 2 {
		panic("expected database path")
	}
	path := os.Args[1]
	contract, err := config.LoadSQLiteContract()
	check(err)
	legacy, err := sql.Open(contract.Driver, path)
	check(err)
	check(createLegacyV5(legacy))
	check(seedLegacyV5(ctx, legacy))
	check(legacy.Close())

	database, err := openCurrent(ctx, path, contract)
	check(err)
	if _, err := database.ExecContext(ctx, `INSERT INTO plugin_replicas(instance_id, replica_id, observed_state, observed_at) VALUES ('fixture', 'replica-fixture', 'pending', ?)`, timestamp); err != nil {
		panic(err)
	}
	store, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	active, pointers, err := store.Current(ctx, "fixture")
	check(err)
	previous, err := store.GetRevision(ctx, "fixture", pointers.PreviousRevision)
	check(err)
	staging, err := store.GetRevision(ctx, "fixture", pointers.PendingRevision)
	check(err)
	operation, err := operationStore.Get(ctx, "operation-fixture")
	check(err)
	payload, found, err := operationStore.Payload(ctx, operation.ID)
	check(err)
	var settingsColumn int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('operation_payloads') WHERE name = 'settings_json'`).Scan(&settingsColumn))
	var legacyTablesRemoved bool
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) = 0 FROM sqlite_master WHERE type = 'table' AND name IN ('plugin_config_revisions', 'plugin_config_pointers')`).Scan(&legacyTablesRemoved))
	var legacyTopologyColumnsRemoved bool
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) = 0 FROM pragma_table_info('plugin_instances') WHERE name IN ('mode', 'endpoint')`).Scan(&legacyTopologyColumnsRemoved))
	var replicaRowsPreserved int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_replicas WHERE instance_id = 'fixture' AND replica_id = 'replica-fixture'`).Scan(&replicaRowsPreserved))
	var foreignKeyViolations int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyViolations))
	check(database.Close())

	database, err = openCurrent(ctx, path, contract)
	check(err)
	defer database.Close()
	reopened, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	reopenedActive, reopenedPointers, err := reopened.Current(ctx, "fixture")
	check(err)
	var migrationVersion int
	check(database.QueryRowContext(ctx, contract.MigrationVersionQuery).Scan(&migrationVersion))
	report := map[string]any{
		"migrationVersion":             migrationVersion,
		"active":                       map[string]any{"generation": active.Revision, "raw": string(active.SettingsJSON)},
		"previous":                     map[string]any{"generation": previous.Revision, "raw": string(previous.SettingsJSON)},
		"staging":                      map[string]any{"generation": staging.Revision, "raw": string(staging.SettingsJSON)},
		"operationState":               operation.State,
		"payloadHasNoSettings":         found && payload.Valid() && settingsColumn == 0,
		"repeatedStartupPreserved":     reopenedActive.Revision == active.Revision && reopenedPointers.PreviousRevision == pointers.PreviousRevision && reopenedPointers.PendingRevision == pointers.PendingRevision,
		"legacyTablesRemoved":          legacyTablesRemoved,
		"legacyTopologyColumnsRemoved": legacyTopologyColumnsRemoved,
		"replicaRowsPreserved":         replicaRowsPreserved == 1,
		"foreignKeysValid":             foreignKeyViolations == 0,
	}
	check(json.NewEncoder(os.Stdout).Encode(report))
}

func createLegacyV5(database *sql.DB) error {
	_, err := database.Exec(`
CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
INSERT INTO schema_migrations(version) VALUES (3), (4), (5);
CREATE TABLE plugin_instances(id TEXT PRIMARY KEY, mode TEXT NOT NULL, endpoint TEXT, settings_json BLOB NOT NULL, manifest_json BLOB NOT NULL, state TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE plugin_config_revisions(instance_id TEXT NOT NULL, revision INTEGER NOT NULL, schema_version INTEGER NOT NULL, digest TEXT NOT NULL, settings_json BLOB NOT NULL, state TEXT NOT NULL, actor TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(instance_id, revision));
CREATE TABLE plugin_config_pointers(instance_id TEXT PRIMARY KEY, current_revision INTEGER, previous_revision INTEGER, pending_revision INTEGER, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE operations(id TEXT PRIMARY KEY, kind TEXT NOT NULL, state TEXT NOT NULL, request_id TEXT NOT NULL, actor TEXT NOT NULL, resource TEXT NOT NULL, result_json BLOB, problem_json BLOB, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE operation_payloads(operation_id TEXT PRIMARY KEY, version INTEGER NOT NULL, resource TEXT NOT NULL, expected_revision INTEGER NOT NULL, schema_version INTEGER NOT NULL, digest TEXT NOT NULL, settings_json BLOB);
`)
	return err
}

func seedLegacyV5(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `INSERT INTO plugin_instances(id, mode, settings_json, manifest_json, state, revision) VALUES ('fixture', 'remote', ?, '{"name":"fixture"}', 'configured', 2)`, []byte(activeRaw)); err != nil {
		return err
	}
	for _, record := range []struct {
		revision int
		raw      string
		state    string
	}{{1, previousRaw, "superseded"}, {2, activeRaw, "active"}} {
		if _, err := database.ExecContext(ctx, `INSERT INTO plugin_config_revisions(instance_id, revision, schema_version, digest, settings_json, state, actor, created_at) VALUES ('fixture', ?, 1, ?, ?, ?, 'admin', ?)`,
			record.revision, digest([]byte(record.raw)), []byte(record.raw), record.state, timestamp); err != nil {
			return err
		}
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO plugin_config_pointers(instance_id, current_revision, previous_revision, pending_revision) VALUES ('fixture', 2, 1, NULL)`); err != nil {
		return err
	}
	payload := models.OperationPayload{Version: 1, Resource: "fixture", ExpectedRevision: 2, SchemaVersion: 1}
	payload.Digest = payload.ComputeDigest([]byte(stagingRaw))
	if _, err := database.ExecContext(ctx, `INSERT INTO operations(id, kind, state, request_id, actor, resource, created_at, updated_at) VALUES ('operation-fixture', 'plugin.settings.apply', 'pending', 'request-fixture', 'admin', 'fixture', ?, ?)`, timestamp, timestamp); err != nil {
		return err
	}
	_, err := database.ExecContext(ctx, `INSERT INTO operation_payloads(operation_id, version, resource, expected_revision, schema_version, digest, settings_json) VALUES ('operation-fixture', ?, ?, ?, ?, ?, ?)`,
		payload.Version, payload.Resource, payload.ExpectedRevision, payload.SchemaVersion, payload.Digest, []byte(stagingRaw))
	return err
}

func openCurrent(ctx context.Context, path string, contract config.SQLiteContract) (*sql.DB, error) {
	return storage.OpenSQLite(ctx, path, storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode, DatabaseFileMode: contract.DatabaseFileMode,
		MaxOpenConnections: contract.MaxOpenConnections, MaxIdleConnections: contract.MaxIdleConnections,
		SchemaVersion: contract.SchemaVersion, HasMigrationTableQuery: contract.HasMigrationTableQuery,
		MigrationVersionQuery: contract.MigrationVersionQuery, SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func check(err error) {
	if err != nil {
		panic(fmt.Sprint(err))
	}
}
