package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
)

type cohortApplier struct {
	targets   []models.ServiceRolloutTarget
	available map[string]bool
	calls     []string
}

func (applier *cohortApplier) ApplyConfiguration(context.Context, string, string, []byte) error {
	return nil
}

func (applier *cohortApplier) CaptureConfigurationTargets(context.Context, string) ([]models.ServiceRolloutTarget, bool, error) {
	return append([]models.ServiceRolloutTarget(nil), applier.targets...), true, nil
}

func (applier *cohortApplier) LostConfigurationTargets(context.Context, string, []models.ServiceRolloutTarget) ([]models.ServiceRolloutTarget, error) {
	return nil, nil
}

func (applier *cohortApplier) ApplyConfigurationToTargets(_ context.Context, _, _, _ string, _ []byte, targets []models.ServiceRolloutTarget) ([]models.ServiceRolloutTarget, error) {
	acknowledged := make([]models.ServiceRolloutTarget, 0, len(targets))
	for _, target := range targets {
		key := target.ReplicaID + "/" + target.IncarnationID
		if target.Acknowledged {
			continue
		}
		if applier.available[key] {
			applier.calls = append(applier.calls, key)
			target.Acknowledged = true
			acknowledged = append(acknowledged, target)
		}
	}
	if len(acknowledged) != len(unacknowledged(targets)) {
		return acknowledged, models.ServiceConfigurationConvergencePending{}
	}
	return acknowledged, nil
}

func main() {
	if len(os.Args) != 3 {
		panic("expected database path and phase")
	}
	ctx := context.Background()
	management, err := config.LoadManagement()
	check(err)
	serviceConfig, err := config.LoadServiceConfiguration()
	check(err)
	sqliteContract, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqliteContract.Driver, ParentDirectoryMode: sqliteContract.ParentDirectoryMode,
		DatabaseFileMode: sqliteContract.DatabaseFileMode, MaxOpenConnections: sqliteContract.MaxOpenConnections,
		MaxIdleConnections: sqliteContract.MaxIdleConnections, SchemaVersion: sqliteContract.SchemaVersion,
		HasMigrationTableQuery: sqliteContract.HasMigrationTableQuery, MigrationVersionQuery: sqliteContract.MigrationVersionQuery,
		SchemaVersionError: sqliteContract.SchemaVersionError, Pragmas: sqliteContract.Pragmas,
	}, sqliteContract.Schema)
	check(err)
	defer database.Close()
	configStore, err := storage.NewSQLiteServiceConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	rolloutStore := configStore
	service := newService(configStore, operationStore, management, serviceConfig)
	phase := os.Args[2]
	var applier *cohortApplier
	switch phase {
	case "rollback-begin":
		seed(ctx, database, configStore)
		_, err = database.ExecContext(ctx, `UPDATE service_config_generations SET slot = 'previous' WHERE instance_id = ? AND generation = 1`, "registered")
		check(err)
		_, err = database.ExecContext(ctx, `INSERT INTO service_config_generations
			(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
			VALUES (?, 2, 'active', ?, ?, 1, ?)`, "registered", []byte(`{"version":2}`),
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", time.Now().UTC().Format(time.RFC3339Nano))
		check(err)
		operation := models.Operation{ID: "operation-rollback-cohort", Kind: management.OperationKinds.ServiceSettingsRollback,
			State: management.Statuses.Pending, RequestID: "request-rollback-cohort", Actor: "operator", Resource: "registered", CreatedAt: time.Now().UTC()}
		check(operationStore.Create(ctx, operation))
		applier = &cohortApplier{
			targets:   []models.ServiceRolloutTarget{target("replica-a", "incarnation-a"), target("replica-b", "incarnation-b")},
			available: map[string]bool{"replica-b/incarnation-b": true},
		}
		service.Applier = applier
		_, registered, err := service.RestorePreviousConfiguration(ctx, operation.ID, "registered", 2,
			models.AuditRecord{Actor: "operator", Action: "rollback", Result: "succeeded", RequestID: operation.RequestID})
		check(err)
		if !registered {
			panic("registered rollback did not capture its exact cohort")
		}
		check(operationStore.Transition(ctx, operation.ID, management.Statuses.Pending, management.Statuses.Running, ""))
		active, _, err := configStore.Current(ctx, "registered")
		check(err)
		applyErr := service.ApplyRestoredConfiguration(ctx, operation.ID, active)
		var pending models.ServiceConfigurationConvergencePending
		if !errors.As(applyErr, &pending) {
			panic("partial rollback did not remain pending convergence")
		}
		fresh, err := operationStore.Get(ctx, operation.ID)
		check(err)
		cohort, found, err := rolloutStore.Targets(ctx, operation.ID)
		check(err)
		if !found {
			panic("rollback exact cohort was not persisted")
		}
		acknowledged := make([]string, 0, len(cohort))
		for _, target := range cohort {
			if target.Acknowledged {
				acknowledged = append(acknowledged, target.ReplicaID+"/"+target.IncarnationID)
			}
		}
		_, pointers, err := configStore.Current(ctx, "registered")
		check(err)
		write(map[string]any{"state": fresh.State, "activeGeneration": pointers.CurrentRevision,
			"previousGeneration": 2, "targets": keys(cohort), "acknowledged": acknowledged})
	case "rollback-recover":
		applier = &cohortApplier{
			targets: []models.ServiceRolloutTarget{target("replica-a", "incarnation-replacement"), target("replica-b", "incarnation-b")},
			available: map[string]bool{"replica-a/incarnation-a": true, "replica-a/incarnation-replacement": true,
				"replica-b/incarnation-b": true},
		}
		service.Applier = applier
		if err := service.RecoverRegistered(ctx, func(string) bool { return true }); err != nil {
			check(err)
		}
		operation, err := operationStore.Get(ctx, "operation-rollback-cohort")
		check(err)
		cohort, found, err := rolloutStore.Targets(ctx, operation.ID)
		check(err)
		if !found {
			panic("recovered rollback exact cohort was not persisted")
		}
		active, _, err := configStore.Current(ctx, "registered")
		check(err)
		write(map[string]any{"state": operation.State, "activeGeneration": active.Revision,
			"targets": keys(cohort), "called": applier.calls, "replacementNeverCalled": !contains(applier.calls, "replica-a/incarnation-replacement")})
	case "begin":
		seed(ctx, database, configStore)
		applier = &cohortApplier{
			targets:   []models.ServiceRolloutTarget{target("replica-a", "incarnation-a"), target("replica-b", "incarnation-b")},
			available: map[string]bool{"replica-b/incarnation-b": true},
		}
		service.Applier = applier
		service.ScheduleWorker = func(worker func()) { worker() }
		operation, err := service.Submit(ctx, command(), reservation(management, serviceConfig))
		check(err)
		fresh, err := operationStore.Get(ctx, operation.ID)
		check(err)
		cohort, found, err := rolloutStore.Targets(ctx, operation.ID)
		check(err)
		if !found {
			panic("rollout cohort was not persisted")
		}
		acknowledgedBeforeClose := make([]string, 0)
		for _, target := range cohort {
			if target.Acknowledged {
				acknowledgedBeforeClose = append(acknowledgedBeforeClose, target.ReplicaID)
			}
		}
		prematureCloseBlocked := false
		if err := rolloutStore.CompleteRollout(ctx, operation.ID); err != nil {
			var conflict models.ServiceConfigurationConflict
			prematureCloseBlocked = errors.As(err, &conflict)
		}
		write(map[string]any{"state": fresh.State, "cohort": keys(cohort), "acknowledged": acknowledgedBeforeClose, "prematureCloseBlocked": prematureCloseBlocked})
	case "replacement", "recovered":
		applier = &cohortApplier{
			targets:   []models.ServiceRolloutTarget{target("replica-b", "incarnation-b"), target("replica-a", "incarnation-replacement")},
			available: map[string]bool{},
		}
		if phase == "replacement" {
			applier.available = map[string]bool{"replica-a/incarnation-replacement": true, "replica-b/incarnation-b": true}
		} else {
			applier.available = map[string]bool{"replica-a/incarnation-a": true, "replica-b/incarnation-b": true}
		}
		applier.calls = []string{}
		service.Applier = applier
		rollbackBlocked := false
		secondRolloutBlocked := false
		if phase == "replacement" {
			_, rollbackErr := configStore.RestorePrevious(ctx, "registered", 2,
				models.AuditRecord{Actor: "operator", Action: "rollback", Resource: "registered", Result: "succeeded", RequestID: "request-rollback"})
			var conflict models.ServiceConfigurationConflict
			rollbackBlocked = errors.As(rollbackErr, &conflict)
			_, candidateErr := configStore.CreateCandidate(ctx, "", "", "", "registered", 2, 1, []byte(`{"version":3}`),
				models.AuditRecord{Actor: "operator", Action: "candidate", Resource: "registered", Result: "pending", RequestID: "request-second-candidate"})
			var candidateConflict models.ServiceConfigurationConflict
			secondRolloutBlocked = errors.As(candidateErr, &candidateConflict)
		}
		check(service.Recover(ctx))
		operation, err := operationStore.Get(ctx, "operation-rollout-cohort")
		check(err)
		cohort, found, err := rolloutStore.Targets(ctx, operation.ID)
		check(err)
		if !found {
			panic("rollout cohort was not persisted")
		}
		result := map[string]any{"state": operation.State, "targets": keys(cohort), "called": applier.calls}
		if phase == "replacement" {
			result["replacementDidNotAcknowledge"] = contains(keys(cohort), "replica-a/incarnation-replacement") == false
			active, _, activeErr := configStore.Current(ctx, "registered")
			check(activeErr)
			result["rollbackBlocked"] = rollbackBlocked
			result["secondRolloutBlocked"] = secondRolloutBlocked
			result["activeGeneration"] = active.Revision
		}
		write(result)
	default:
		panic("unknown phase")
	}
}

func newService(configStore *storage.SQLiteServiceConfigurationStore, operationStore *storage.SQLiteOperationStore, management config.ManagementWords, serviceConfig config.ServiceConfigurationWords) *application.ServiceConfigurationService {
	return &application.ServiceConfigurationService{
		Store: configStore, Operations: application.OperationService{Store: operationStore},
		OperationKind:         management.OperationKinds.ServiceSettingsApply,
		RollbackOperationKind: management.OperationKinds.ServiceSettingsRollback,
		OperationStates:       application.ServiceConfigurationOperationStates{Pending: management.Statuses.Pending, Running: management.Statuses.Running, Succeeded: management.Statuses.Succeeded, Failed: management.Statuses.Failed},
		OperationFailureCodes: application.ServiceConfigurationFailureCodes{ApplyFailed: management.Codes.ActivationFailed},
		RevisionStates:        application.ServiceConfigurationRevisionStates{Active: serviceConfig.Slots.Active, Candidate: serviceConfig.Slots.Staging},
		PayloadVersion:        serviceConfig.SchemaVersion, MaximumPayloadBytes: serviceConfig.MaximumPayloadBytes,
		PayloadFailureCode: management.Codes.ActivationFailed,
	}
}

func seed(ctx context.Context, database *sql.DB, configStore *storage.SQLiteServiceConfigurationStore) {
	_, err := database.ExecContext(ctx, `INSERT INTO service_instances (id, manifest_json, state) VALUES (?, ?, ?)`, "registered", []byte(`{}`), "configured")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO service_registered_instances(instance_id) VALUES (?)`, "registered")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO service_config_generations (instance_id, generation, slot, raw_json, sha256, schema_version, created_at) VALUES (?, 1, 'active', ?, ?, 1, ?)`, "registered", []byte(`{"version":1}`), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now().UTC().Format(time.RFC3339Nano))
	check(err)
	_ = configStore
}

func command() application.ApplyServiceConfigurationCommand {
	raw := []byte(`{ "version" : 2 }`)
	return application.ApplyServiceConfigurationCommand{
		OperationID: "operation-rollout-cohort", InstanceID: "registered", ExpectedRevision: 1, SchemaVersion: 1, SettingsJSON: raw,
		CandidateAudit: models.AuditRecord{Actor: "operator", Action: "candidate", Result: "pending", RequestID: "request-rollout-cohort"},
		AppliedAudit:   models.AuditRecord{Actor: "operator", Action: "apply", Result: "succeeded", RequestID: "request-rollout-cohort"},
	}
}

func reservation(management config.ManagementWords, serviceConfig config.ServiceConfigurationWords) models.OperationReservation {
	raw := []byte(`{ "version" : 2 }`)
	payload := models.OperationPayload{Version: serviceConfig.SchemaVersion, Resource: "registered", ExpectedRevision: 1, SchemaVersion: 1}
	payload.Digest = payload.ComputeDigest(raw)
	return models.OperationReservation{
		Operation: models.Operation{ID: "operation-rollout-cohort", Kind: management.OperationKinds.ServiceSettingsApply, State: management.Statuses.Pending, RequestID: "request-rollout-cohort", Actor: "operator", Resource: "registered", CreatedAt: time.Now().UTC()},
		Scope:     "service-settings:registered", Key: "rollout-cohort-key", RequestDigest: payload.Digest, Payload: &payload,
	}
}

func target(replicaID, incarnationID string) models.ServiceRolloutTarget {
	return models.ServiceRolloutTarget{ReplicaID: replicaID, IncarnationID: incarnationID,
		ReleaseSHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		LeaseExpiresAt: time.Now().UTC().Add(time.Minute)}
}

func unacknowledged(targets []models.ServiceRolloutTarget) []models.ServiceRolloutTarget {
	result := make([]models.ServiceRolloutTarget, 0, len(targets))
	for _, target := range targets {
		if !target.Acknowledged {
			result = append(result, target)
		}
	}
	return result
}

func keys(targets []models.ServiceRolloutTarget) []string {
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		result = append(result, target.ReplicaID+"/"+target.IncarnationID)
	}
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func write(value any) {
	check(json.NewEncoder(os.Stdout).Encode(value))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
