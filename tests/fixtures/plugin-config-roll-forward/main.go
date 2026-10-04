package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type missingAcknowledgement struct {
	store       *storage.SQLitePluginConfigurationStore
	sawActive   int64
	sawPrevious int64
}

func (applier *missingAcknowledgement) ApplyConfiguration(ctx context.Context, instanceID, _ string, _ []byte) error {
	_, pointers, err := applier.store.Current(ctx, instanceID)
	if err != nil {
		return err
	}
	applier.sawActive = pointers.CurrentRevision
	applier.sawPrevious = pointers.PreviousRevision
	return errors.New("replica did not acknowledge reload")
}

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
	_, err = database.ExecContext(ctx, "INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)", "fixture", []byte(`{"name":"fixture"}`), "configured")
	check(err)
	initial := []byte(`{"origin":"before"}`)
	digest := sha256.Sum256(initial)
	_, err = database.ExecContext(ctx, "INSERT INTO plugin_config_generations (instance_id,generation,slot,raw_json,sha256,schema_version,created_at) VALUES (?,?,?,?,?,1,?)", "fixture", 1, "active", initial, hex.EncodeToString(digest[:]), time.Now().UTC().Format(time.RFC3339Nano))
	check(err)
	store, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	configuration, err := config.LoadPluginConfiguration()
	check(err)
	applier := &missingAcknowledgement{store: store}
	service := application.PluginConfigurationService{Store: store, Applier: applier, Unavailable: "unavailable",
		RevisionStates: application.PluginConfigurationRevisionStates{Active: configuration.Slots.Active, Candidate: configuration.Slots.Staging}}
	_, applyErr := service.Apply(ctx, application.ApplyPluginConfigurationCommand{
		InstanceID: "fixture", ExpectedRevision: 1, SchemaVersion: 1, SettingsJSON: []byte(`{"origin":"after"}`),
		CandidateAudit: audit("candidate", "succeeded"), AppliedAudit: audit("applied", "succeeded"), FailedAudit: audit("failed", "failed"),
	})
	if applyErr == nil {
		panic("missing reload ACK unexpectedly succeeded")
	}
	current, pointers, err := store.Current(ctx, "fixture")
	check(err)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"reloadSawActive": applier.sawActive, "reloadSawPrevious": applier.sawPrevious,
		"activeAfterMissingAck": current.Revision, "previousAfterMissingAck": pointers.PreviousRevision,
		"stagingAfterMissingAck": pointers.PendingRevision != 0, "instanceFenced": service.IsInstanceFenced("fixture"),
	}))
}

func audit(action, result string) models.AuditRecord {
	return models.AuditRecord{Timestamp: time.Now().UTC(), Actor: "operator", Action: action, Resource: "fixture", Result: result, RequestID: action}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
