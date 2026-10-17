package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
)

const releaseDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type targetLossApplier struct {
	target     models.ServiceRolloutTarget
	now        time.Time
	dispatches int
	lateReload bool
}

func (applier *targetLossApplier) ApplyConfiguration(context.Context, string, string, []byte) error {
	return nil
}

func (applier *targetLossApplier) CaptureConfigurationTargets(context.Context, string) ([]models.ServiceRolloutTarget, bool, error) {
	return []models.ServiceRolloutTarget{applier.target}, true, nil
}

func (applier *targetLossApplier) LostConfigurationTargets(_ context.Context, _ string, targets []models.ServiceRolloutTarget) ([]models.ServiceRolloutTarget, error) {
	if applier.now.Before(applier.target.LeaseExpiresAt) {
		return nil, nil
	}
	return append([]models.ServiceRolloutTarget(nil), targets...), nil
}

func (applier *targetLossApplier) ApplyConfigurationToTargets(_ context.Context, _, _, _ string, _ []byte, targets []models.ServiceRolloutTarget) ([]models.ServiceRolloutTarget, error) {
	applier.dispatches++
	applier.lateReload = !applier.now.Before(applier.target.LeaseExpiresAt)
	return nil, models.ServiceConfigurationConvergencePending{}
}

func main() {
	if len(os.Args) != 2 {
		panic("expected database path")
	}
	ctx := context.Background()
	management, err := config.LoadManagement()
	check(err)
	serviceConfig, err := config.LoadServiceConfiguration()
	check(err)
	sqlite := mustSQLiteContract()
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqlite.Driver, ParentDirectoryMode: sqlite.ParentDirectoryMode,
		DatabaseFileMode: sqlite.DatabaseFileMode, MaxOpenConnections: sqlite.MaxOpenConnections,
		MaxIdleConnections: sqlite.MaxIdleConnections, SchemaVersion: sqlite.SchemaVersion,
		HasMigrationTableQuery: sqlite.HasMigrationTableQuery, MigrationVersionQuery: sqlite.MigrationVersionQuery,
		SchemaVersionError: sqlite.SchemaVersionError, Pragmas: sqlite.Pragmas,
	}, sqlite.Schema)
	check(err)
	defer database.Close()
	configStore, err := storage.NewSQLiteServiceConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO service_instances (id, manifest_json, state) VALUES (?, ?, ?)`, "registered", []byte(`{}`), "configured")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO service_registered_instances(instance_id) VALUES (?)`, "registered")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO service_config_generations
		(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
		VALUES (?, 1, 'active', ?, ?, 1, ?)`, "registered", []byte(`{"version":1}`), releaseDigest, time.Now().UTC().Format(time.RFC3339Nano))
	check(err)

	expiresAt := time.Now().UTC().Add(200 * time.Millisecond)
	applier := &targetLossApplier{
		target: models.ServiceRolloutTarget{ReplicaID: "replica-a", IncarnationID: "incarnation-a",
			ReleaseSHA256: releaseDigest, LeaseExpiresAt: expiresAt},
		now: time.Now().UTC(),
	}
	service := &application.ServiceConfigurationService{
		Store: configStore, Applier: applier, Operations: application.OperationService{Store: operationStore},
		OperationKind: management.OperationKinds.ServiceSettingsApply,
		OperationStates: application.ServiceConfigurationOperationStates{Pending: management.Statuses.Pending,
			Running: management.Statuses.Running, Succeeded: management.Statuses.Succeeded, Failed: management.Statuses.Failed},
		OperationFailureCodes: application.ServiceConfigurationFailureCodes{ApplyFailed: management.Codes.ActivationFailed,
			TargetLost: management.Codes.TargetLost},
		RevisionStates: application.ServiceConfigurationRevisionStates{Active: serviceConfig.Slots.Active,
			Candidate: serviceConfig.Slots.Staging},
		PayloadVersion: serviceConfig.SchemaVersion, MaximumPayloadBytes: serviceConfig.MaximumPayloadBytes,
		PayloadFailureCode: management.Codes.ActivationFailed,
	}
	var workers []func()
	service.ScheduleWorker = func(worker func()) { workers = append(workers, worker) }
	raw := []byte(`{"version":2}`)
	payload := models.OperationPayload{Version: serviceConfig.SchemaVersion, Resource: "registered", ExpectedRevision: 1, SchemaVersion: 1}
	payload.Digest = payload.ComputeDigest(raw)
	reservation := models.OperationReservation{
		Operation: models.Operation{ID: "operation-target-lost", Kind: management.OperationKinds.ServiceSettingsApply,
			State: management.Statuses.Pending, RequestID: "request-target-lost", Actor: "operator", Resource: "registered", CreatedAt: time.Now().UTC()},
		Scope: "service-settings:registered", Key: "target-lost-key", RequestDigest: payload.Digest, Payload: &payload,
	}
	operation, err := service.Submit(ctx, application.ApplyServiceConfigurationCommand{
		InstanceID: "registered", ExpectedRevision: 1, SchemaVersion: 1, SettingsJSON: raw,
		CandidateAudit: models.AuditRecord{Actor: "operator", Action: "candidate", Result: "pending", RequestID: reservation.Operation.RequestID},
		AppliedAudit:   models.AuditRecord{Actor: "operator", Action: "apply", Result: "succeeded", RequestID: reservation.Operation.RequestID},
		FailedAudit:    models.AuditRecord{Actor: "operator", Action: "apply_failed", Result: "failed", RequestID: reservation.Operation.RequestID},
	}, reservation)
	check(err)
	if len(workers) != 1 {
		panic("expected one scheduled rollout worker")
	}
	workers[0]()
	if applier.dispatches != 1 {
		panic("expected exactly one initial dispatch")
	}
	applier.now = expiresAt.Add(time.Second)
	check(service.RecoverRegistered(ctx, func(string) bool { return true }))
	operation, err = operationStore.Get(ctx, operation.ID)
	check(err)
	active, _, err := configStore.Current(ctx, "registered")
	check(err)
	var open bool
	check(database.QueryRowContext(ctx, `SELECT open FROM service_rollouts WHERE operation_id = ?`, operation.ID).Scan(&open))
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"state": operation.State, "errorCode": operation.ErrorCode,
		"activeGeneration": active.Revision, "rolloutOpen": open,
		"reloadCount": applier.dispatches, "reloadAfterLeaseExpiry": applier.lateReload,
	}))
}

func mustSQLiteContract() config.SQLiteContract {
	contract, err := config.LoadSQLiteContract()
	check(err)
	return contract
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
