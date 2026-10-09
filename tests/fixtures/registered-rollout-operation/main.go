package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type partialApplier struct {
	calls         int
	failFirst     bool
	genIDsApplied []string
}

func (applier *partialApplier) ApplyConfiguration(_ context.Context, _ string, generation string, _ []byte) error {
	applier.calls++
	applier.genIDsApplied = append(applier.genIDsApplied, generation)
	if applier.failFirst && applier.calls == 1 {
		return models.PluginConfigurationConvergencePending{}
	}
	return nil
}

func main() {
	ctx := context.Background()
	management, err := config.LoadManagement()
	check(err)
	pluginConfig, err := config.LoadPluginConfiguration()
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
	if len(os.Args) != 3 {
		panic("expected database path and phase")
	}
	configStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	service := newService(configStore, operationStore, management, pluginConfig)
	if os.Args[2] == "recover" {
		recoverAfterRestart(ctx, service, operationStore)
		return
	}
	if os.Args[2] != "begin" {
		panic("unknown phase")
	}
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)`,
		"registered", []byte(`{}`), "configured")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_config_generations
		(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
		VALUES (?, 1, 'active', ?, ?, 1, ?)`, "registered", []byte(`{"version":1}`),
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now().UTC().Format(time.RFC3339Nano))
	check(err)
	var workers []func()
	applier := &partialApplier{failFirst: true}
	service.Applier = applier
	service.ScheduleWorker = func(worker func()) { workers = append(workers, worker) }
	raw := []byte(`{ "version" : 2 }`)
	payload := models.OperationPayload{
		Version: pluginConfig.SchemaVersion, Resource: "registered", ExpectedRevision: 1, SchemaVersion: 1,
	}
	payload.Digest = payload.ComputeDigest(raw)
	reservation := models.OperationReservation{
		Operation: models.Operation{
			ID: "operation-registered-rollout", Kind: management.OperationKinds.PluginSettingsApply,
			State: management.Statuses.Pending, RequestID: "request-registered-rollout",
			Actor: "operator", Resource: "registered", CreatedAt: time.Now().UTC(),
		},
		Scope: "plugin-settings:registered", Key: "registered-rollout-key", RequestDigest: payload.Digest, Payload: &payload,
	}
	operation, err := service.Submit(ctx, application.ApplyPluginConfigurationCommand{
		InstanceID: "registered", ExpectedRevision: 1, SchemaVersion: 1, SettingsJSON: raw,
		CandidateAudit: models.AuditRecord{Actor: "operator", Action: "candidate", Result: "pending", RequestID: "request-registered-rollout"},
		AppliedAudit:   models.AuditRecord{Actor: "operator", Action: "apply", Result: "succeeded", RequestID: "request-registered-rollout"},
		FailedAudit:    models.AuditRecord{Actor: "operator", Action: "apply_failed", Result: "failed", RequestID: "request-registered-rollout"},
	}, reservation)
	check(err)
	if len(workers) != 1 {
		panic("rollout worker was not scheduled")
	}
	workers[0]()
	current, err := operationStore.Get(ctx, operation.ID)
	check(err)
	active, _, err := configStore.Current(ctx, "registered")
	check(err)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"stateAfterPartialAck": current.State, "activeGeneration": active.Revision,
	}))
}

func newService(configStore *storage.SQLitePluginConfigurationStore, operationStore *storage.SQLiteOperationStore, management config.ManagementWords, pluginConfig config.PluginConfigurationWords) *application.PluginConfigurationService {
	return &application.PluginConfigurationService{
		Store: configStore, Operations: application.OperationService{Store: operationStore},
		OperationKind: management.OperationKinds.PluginSettingsApply,
		OperationStates: application.PluginConfigurationOperationStates{
			Pending: management.Statuses.Pending, Running: management.Statuses.Running,
			Succeeded: management.Statuses.Succeeded, Failed: management.Statuses.Failed,
		},
		OperationFailureCodes: application.PluginConfigurationFailureCodes{ApplyFailed: management.Codes.ActivationFailed},
		RevisionStates:        application.PluginConfigurationRevisionStates{Active: pluginConfig.Slots.Active, Candidate: pluginConfig.Slots.Staging},
		PayloadVersion:        pluginConfig.SchemaVersion, MaximumPayloadBytes: pluginConfig.MaximumPayloadBytes,
		PayloadFailureCode: management.Codes.ActivationFailed,
	}
}

func recoverAfterRestart(ctx context.Context, service *application.PluginConfigurationService, operationStore *storage.SQLiteOperationStore) {
	operationID := "operation-registered-rollout"
	before, err := operationStore.Get(ctx, operationID)
	check(err)
	applier := &partialApplier{}
	service.Applier = applier
	if err := service.RecoverRegistered(ctx, func(string) bool { return false }); err != nil {
		check(err)
	}
	unregistered, err := operationStore.Get(ctx, operationID)
	check(err)
	callsBeforeRegistration := applier.calls
	if unregistered.State != before.State || callsBeforeRegistration != 0 {
		panic("restart recovery retried before authenticated replica registration")
	}
	if err := service.RecoverRegistered(ctx, func(string) bool { return true }); err != nil {
		check(err)
	}
	completed, err := operationStore.Get(ctx, operationID)
	check(err)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"stateBeforeRegistration": before.State, "didNotRetryBeforeRegistration": unregistered.State == before.State && callsBeforeRegistration == 0,
		"recoveredState": completed.State, "exactGenerationRetried": len(applier.genIDsApplied) == 1 && applier.genIDsApplied[0] == "2",
	}))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
