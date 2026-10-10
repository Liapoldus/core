package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
)

func main() {
	ctx := context.Background()
	contract, err := config.LoadSQLiteContract()
	check(err)
	databasePath := os.Args[1]
	database, err := openDatabase(ctx, databasePath, contract)
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances
		(id, manifest_json, state) VALUES (?, ?, 'configured')`, "fixture", []byte(`{"name":"fixture"}`))
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_config_generations
		(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
		VALUES (?, 1, 'active', ?, ?, 1, '2026-09-29T00:00:00Z')`,
		"fixture", []byte(`{"origin":"old"}`), "d1c2fa5dcee07ed2483d0f5ab8e03cadbd8405d8d7f2fe1c4aa05fb6ca3c0a5b")
	check(err)

	store, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	initial, _, err := store.Current(ctx, "fixture")
	check(err)
	candidate, err := store.CreateCandidate(ctx, "", "", "", "fixture", 1, 1, []byte(`{"origin":"new"}`), audit("candidate"))
	check(err)
	beforeAck, pointers, err := store.Current(ctx, "fixture")
	check(err)
	_, err = store.ActivateCandidate(ctx, "fixture", candidate.Revision, 1, audit("activate"))
	check(err)
	_, activePointers, err := store.Current(ctx, "fixture")
	check(err)
	failedCandidate, err := store.CreateCandidate(ctx, "", "", "", "fixture", 2, 1, []byte(`{"origin":"failed"}`), audit("candidate-failed"))
	check(err)
	_, err = store.FailCandidate(ctx, "fixture", failedCandidate.Revision, 2, audit("fail"))
	check(err)
	currentAfterFailure, pointersAfterFailure, err := store.Current(ctx, "fixture")
	check(err)
	var failedCandidateCount int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_config_generations WHERE instance_id = ? AND generation = ?`, "fixture", failedCandidate.Revision).Scan(&failedCandidateCount))
	_, staleErr := store.CreateCandidate(ctx, "", "", "", "fixture", 1, 1, []byte(`{"origin":"stale"}`), audit("stale"))
	_, err = store.RestorePrevious(ctx, "fixture", 2, audit("restore"))
	check(err)
	_, restoredPointers, err := store.Current(ctx, "fixture")
	check(err)
	pendingCandidate, err := store.CreateCandidate(ctx, "", "", "", "fixture", 1, 1, []byte(`{"origin":"pending-after-reopen"}`), audit("candidate-pending"))
	check(err)
	check(database.Close())

	database, err = openDatabase(ctx, databasePath, contract)
	check(err)
	defer database.Close()
	reopenedStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	reopened, reopenedPointers, err := reopenedStore.Current(ctx, "fixture")
	check(err)
	reopenedPending, err := reopenedStore.GetRevision(ctx, "fixture", pendingCandidate.Revision)
	check(err)
	_, missingRevisionErr := reopenedStore.GetRevision(ctx, "fixture", pendingCandidate.Revision+1)
	digest := sha256.Sum256(reopened.SettingsJSON)

	report := map[string]any{
		"initialRevision":                    initial.Revision,
		"candidateRevision":                  candidate.Revision,
		"candidateStayedPending":             beforeAck.Revision == initial.Revision && pointers.CurrentRevision == 1 && pointers.PendingRevision == candidate.Revision,
		"activatedCurrent":                   activePointers.CurrentRevision,
		"activatedPrevious":                  activePointers.PreviousRevision,
		"failedCandidateDidNotChangeCurrent": currentAfterFailure.Revision == 2 && pointersAfterFailure.CurrentRevision == 2 && pointersAfterFailure.PreviousRevision == 1 && pointersAfterFailure.PendingRevision == 0,
		"failedCandidateRemoved":             failedCandidateCount == 0,
		"staleRevisionRejected":              errors.As(staleErr, new(models.PluginConfigurationConflict)),
		"restoredCurrent":                    restoredPointers.CurrentRevision,
		"restoredPrevious":                   restoredPointers.PreviousRevision,
		"reopenedCurrent":                    reopened.Revision,
		"reopenedPending":                    reopenedPointers.PendingRevision,
		"reopenedPendingState":               reopenedPending.State,
		"reopenedPendingSettings":            string(reopenedPending.SettingsJSON),
		"missingRevisionRejected":            errors.As(missingRevisionErr, new(models.PluginConfigurationNotFound)),
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
