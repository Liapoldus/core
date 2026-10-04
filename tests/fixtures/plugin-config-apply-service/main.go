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
	cancel   context.CancelFunc
}

func (a *applier) ApplyConfiguration(ctx context.Context, _ string, revision string, config []byte) error {
	if a.reject {
		if a.cancel != nil {
			a.cancel()
		}
		return errors.New("apply rejected")
	}
	if a.cancel != nil {
		a.cancel()
	}
	if err := ctx.Err(); err != nil {
		return err
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
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)`,
		"fixture", []byte(`{"name":"fixture"}`), "configured")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_config_generations
		(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
		VALUES (?, 1, 'active', ?, ?, 1, '2026-09-29T00:00:00Z')`,
		"fixture", []byte(`{"origin":"old"}`), "d1c2fa5dcee07ed2483d0f5ab8e03cadbd8405d8d7f2fe1c4aa05fb6ca3c0a5b")
	check(err)
	store, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	pluginConfiguration, err := config.LoadPluginConfiguration()
	check(err)
	client := &applier{}
	service := application.PluginConfigurationService{
		Store: store, Applier: client,
		RevisionStates: application.PluginConfigurationRevisionStates{
			Active: pluginConfiguration.Slots.Active, Candidate: pluginConfiguration.Slots.Staging,
		},
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
	var failedCount int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_config_generations WHERE instance_id = ? AND generation = ?`, "fixture", 3).Scan(&failedCount))
	current, activePointers, err := store.Current(ctx, "fixture")
	check(err)
	client.reject = false
	cancelContext, cancel := context.WithCancel(ctx)
	client.cancel = cancel
	_, cancelled := service.Apply(cancelContext, application.ApplyPluginConfigurationCommand{
		InstanceID: "fixture", ExpectedRevision: 3, SchemaVersion: 1,
		SettingsJSON:   []byte(`{"origin":"cancelled"}`),
		CandidateAudit: audit("configuration_candidate", "pending"),
		AppliedAudit:   audit("configuration_applied", "succeeded"),
		FailedAudit:    audit("configuration_failed", "failed"),
	})
	cancel()
	client.cancel = nil
	var cancelledCount int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_config_generations WHERE instance_id = ? AND generation = ?`, "fixture", 4).Scan(&cancelledCount))
	_, afterCancelledPointers, err := store.Current(ctx, "fixture")
	check(err)
	_, stale := service.Apply(ctx, application.ApplyPluginConfigurationCommand{
		InstanceID: "fixture", ExpectedRevision: 1, SchemaVersion: 1,
		SettingsJSON:   []byte(`{"origin":"stale"}`),
		CandidateAudit: audit("configuration_candidate", "pending"),
		AppliedAudit:   audit("configuration_applied", "succeeded"),
		FailedAudit:    audit("configuration_failed", "failed"),
	})
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"appliedRevision": active.Revision, "activeAfterApply": pointers.CurrentRevision,
		"previousAfterApply": pointers.PreviousRevision, "acknowledgedConfig": string(client.config),
		"failedRevision": 3, "activeAfterReject": activePointers.CurrentRevision,
		"failedCandidateRemoved": failedCount == 0, "staleRevisionRejected": errors.As(stale, new(models.PluginConfigurationConflict)),
		"activeConfig": string(current.SettingsJSON), "cancelledApplyReturnedError": cancelled != nil,
		"cancelledCandidateRemoved": cancelledCount == 0, "currentAfterCancelledApply": afterCancelledPointers.CurrentRevision,
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
