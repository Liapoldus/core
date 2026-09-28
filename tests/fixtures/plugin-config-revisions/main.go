package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func main() {
	ctx := context.Background()
	contract, err := config.LoadSQLiteContract()
	check(err)
	databasePath := os.Args[1]
	database, err := openDatabase(ctx, databasePath, contract)
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances
		(id, mode, settings_json, manifest_json, state, revision)
		VALUES (?, 'remote', ?, ?, 'configured', 1)`, "fixture", []byte(`{"origin":"old"}`), []byte(`{"name":"fixture"}`))
	check(err)

	store, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	initial, _, err := store.Current(ctx, "fixture")
	check(err)
	candidate, err := store.CreateCandidate(ctx, "fixture", 1, 1, []byte(`{"origin":"new"}`), audit("candidate"))
	check(err)
	beforeAck, pointers, err := store.Current(ctx, "fixture")
	check(err)
	_, err = store.ActivateCandidate(ctx, "fixture", candidate.Revision, 1, audit("activate"))
	check(err)
	_, activePointers, err := store.Current(ctx, "fixture")
	check(err)
	failedCandidate, err := store.CreateCandidate(ctx, "fixture", 2, 1, []byte(`{"origin":"failed"}`), audit("candidate-failed"))
	check(err)
	_, err = store.FailCandidate(ctx, "fixture", failedCandidate.Revision, 2, audit("fail"))
	check(err)
	currentAfterFailure, pointersAfterFailure, err := store.Current(ctx, "fixture")
	check(err)
	var failedCandidateState string
	check(database.QueryRowContext(ctx, `SELECT state FROM plugin_config_revisions WHERE instance_id = ? AND revision = ?`, "fixture", failedCandidate.Revision).Scan(&failedCandidateState))
	_, staleErr := store.CreateCandidate(ctx, "fixture", 1, 1, []byte(`{"origin":"stale"}`), audit("stale"))
	_, err = store.RestorePrevious(ctx, "fixture", 2, audit("restore"))
	check(err)
	_, restoredPointers, err := store.Current(ctx, "fixture")
	check(err)
	check(database.Close())

	database, err = openDatabase(ctx, databasePath, contract)
	check(err)
	defer database.Close()
	reopenedStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	reopened, _, err := reopenedStore.Current(ctx, "fixture")
	check(err)
	digest := sha256.Sum256(reopened.SettingsJSON)

	report := map[string]any{
		"initialRevision":                    initial.Revision,
		"candidateRevision":                  candidate.Revision,
		"candidateStayedPending":             beforeAck.Revision == initial.Revision && pointers.CurrentRevision == 1 && pointers.PendingRevision == candidate.Revision,
		"activatedCurrent":                   activePointers.CurrentRevision,
		"activatedPrevious":                  activePointers.PreviousRevision,
		"failedCandidateDidNotChangeCurrent": currentAfterFailure.Revision == 2 && pointersAfterFailure.CurrentRevision == 2 && pointersAfterFailure.PreviousRevision == 1 && pointersAfterFailure.PendingRevision == 0,
		"failedCandidateState":               failedCandidateState,
		"staleRevisionRejected":              errors.As(staleErr, new(models.PluginConfigurationConflict)),
		"restoredCurrent":                    restoredPointers.CurrentRevision,
		"restoredPrevious":                   restoredPointers.PreviousRevision,
		"reopenedCurrent":                    reopened.Revision,
		"digestMatchesDocument":              hex.EncodeToString(digest[:]) == reopened.Digest,
	}
	check(json.NewEncoder(os.Stdout).Encode(report))
}

func openDatabase(ctx context.Context, path string, contract config.SQLiteContract) (*sql.DB, error) {
	return storage.OpenSQLite(ctx, path, storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
}

func audit(action string) models.AuditRecord {
	return models.AuditRecord{
		Actor: "fixture", Action: action, Resource: "fixture",
		Result: "success", RequestID: action,
	}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
