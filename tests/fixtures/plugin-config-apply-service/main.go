package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type applier struct {
	config   []byte
	revision string
	reject   bool
}

func (a *applier) ApplyConfiguration(_ context.Context, _ string, revision string, config []byte) error {
	if a.reject {
		return errors.New("apply rejected")
	}
	a.config = append(a.config[:0], config...)
	a.revision = revision
	return nil
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
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances (id, mode, settings_json, manifest_json, state, revision) VALUES (?, ?, ?, ?, ?, ?)`,
		"fixture", "local", []byte(`{"origin":"old"}`), []byte(`{"name":"fixture"}`), "configured", 1)
	check(err)
	store, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	client := &applier{}
	service := application.PluginConfigurationService{
		Store: store, Applier: client,
	}
	active, err := service.Apply(ctx, application.ApplyPluginConfigurationCommand{
		InstanceID: "fixture", ExpectedRevision: 1, SchemaVersion: 1,
		SettingsJSON:   []byte(`{"origin":"new"}`),
		CandidateAudit: audit("configuration_candidate", "pending"),
		AppliedAudit:   audit("configuration_applied", "succeeded"),
		FailedAudit:    audit("configuration_failed", "failed"),
	})
	check(err)
	_, pointers, err := store.Current(ctx, "fixture")
	check(err)
	client.reject = true
	_, rejected := service.Apply(ctx, application.ApplyPluginConfigurationCommand{
		InstanceID: "fixture", ExpectedRevision: 2, SchemaVersion: 1,
		SettingsJSON:   []byte(`{"origin":"rejected"}`),
		CandidateAudit: audit("configuration_candidate", "pending"),
		AppliedAudit:   audit("configuration_applied", "succeeded"),
		FailedAudit:    audit("configuration_failed", "failed"),
	})
	if rejected == nil {
		panic("rejected apply was accepted")
	}
	var failedState string
	check(database.QueryRowContext(ctx, `SELECT state FROM plugin_config_revisions WHERE instance_id = ? AND revision = ?`, "fixture", 3).Scan(&failedState))
	current, activePointers, err := store.Current(ctx, "fixture")
	check(err)
	_, stale := service.Apply(ctx, application.ApplyPluginConfigurationCommand{
		InstanceID: "fixture", ExpectedRevision: 1, SchemaVersion: 1,
		SettingsJSON:   []byte(`{"origin":"stale"}`),
		CandidateAudit: audit("configuration_candidate", "pending"),
		AppliedAudit:   audit("configuration_applied", "succeeded"),
		FailedAudit:    audit("configuration_failed", "failed"),
	})
	_, err = store.GetRevision(ctx, "fixture", active.Revision)
	check(err)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"appliedRevision": active.Revision, "activeAfterApply": pointers.CurrentRevision,
		"previousAfterApply": pointers.PreviousRevision, "acknowledgedConfig": string(client.config),
		"failedRevision": 3, "activeAfterReject": activePointers.CurrentRevision,
		"failedState": failedState, "staleRevisionRejected": errors.As(stale, new(models.PluginConfigurationConflict)),
		"activeConfig": string(current.SettingsJSON),
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
