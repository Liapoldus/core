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

const candidateDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const incumbentDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type replicaSource struct {
	replicas []models.TrafficRolloutReplica
}

func (source replicaSource) TrafficRolloutReplicas(context.Context, string) ([]models.TrafficRolloutReplica, error) {
	return append([]models.TrafficRolloutReplica(nil), source.replicas...), nil
}

type configValidator struct{}

func (configValidator) ValidateTrafficRolloutConfiguration(_ context.Context, _ string, raw []byte) error {
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil || document == nil {
		return models.TrafficRolloutInvalid{}
	}
	return nil
}

type rolloutApplier struct {
	calls   [][]string
	partial bool
	lost    bool
}

func (*rolloutApplier) CaptureConfigurationTargets(context.Context, string) ([]models.PluginRolloutTarget, bool, error) {
	return nil, false, nil
}

func (applier *rolloutApplier) LostConfigurationTargets(_ context.Context, _ string, targets []models.PluginRolloutTarget) ([]models.PluginRolloutTarget, error) {
	if !applier.lost {
		return nil, nil
	}
	lost := make([]models.PluginRolloutTarget, 0, len(targets))
	for _, target := range targets {
		if !target.Acknowledged {
			lost = append(lost, target)
		}
	}
	return lost, nil
}

func (applier *rolloutApplier) ApplyConfigurationToTargets(_ context.Context, _, _, _ string, _ []byte, targets []models.PluginRolloutTarget) ([]models.PluginRolloutTarget, error) {
	called := make([]string, 0, len(targets))
	for _, target := range targets {
		if !target.Acknowledged {
			called = append(called, target.ReplicaID+"/"+target.IncarnationID)
		}
	}
	applier.calls = append(applier.calls, called)
	acknowledged := make([]models.PluginRolloutTarget, 0, len(called))
	for _, target := range targets {
		if target.Acknowledged || (applier.partial && target.ReplicaID == "candidate-b") {
			continue
		}
		target.Acknowledged = true
		acknowledged = append(acknowledged, target)
	}
	if applier.partial {
		applier.partial = false
		return acknowledged, models.PluginConfigurationConvergencePending{}
	}
	return acknowledged, nil
}

func main() {
	ctx := context.Background()
	contract, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode, DatabaseFileMode: contract.DatabaseFileMode,
		MaxOpenConnections: contract.MaxOpenConnections, MaxIdleConnections: contract.MaxIdleConnections,
		SchemaVersion: contract.SchemaVersion, HasMigrationTableQuery: contract.HasMigrationTableQuery,
		MigrationVersionQuery: contract.MigrationVersionQuery, SchemaVersionError: contract.SchemaVersionError,
		IntegrityCheckQuery: contract.IntegrityCheckQuery, ForeignKeyCheckQuery: contract.ForeignKeyCheckQuery,
		IntegritySuccess: contract.IntegritySuccess, IntegrityError: contract.IntegrityError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	check(err)
	defer database.Close()
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances(id, manifest_json, state) VALUES (?, ?, ?)`, "forms", []byte(`{}`), "ready")
	check(err)
	trafficStore, err := storage.NewSQLiteTrafficRolloutStore(database)
	check(err)
	configurationStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	configuration := []byte(`{"fields":["email"]}`)
	plan := []byte(`{"releaseSha256":"` + candidateDigest + `","targets":[{"replicaId":"candidate-a","incarnation":"inc-a"},{"replicaId":"candidate-b","incarnation":"inc-b"}],"stages":[{"id":"canary","candidateWeightPercent":10,"minimumObservationSeconds":30},{"id":"full","candidateWeightPercent":100,"minimumObservationSeconds":0}]}`)
	reservation := models.OperationReservation{
		Operation: models.Operation{ID: "operation-1", Kind: "traffic-rollout", State: "pending", CreatedAt: now, RequestID: "request-1", Actor: "operator", Resource: "forms"},
		Scope:     "/api/plugins/forms/rollouts", Key: "rollout-key", RequestDigest: digest([]byte("same request")),
	}
	applier := &rolloutApplier{partial: true}
	service := &application.TrafficRolloutService{
		Store: trafficStore, ConfigurationStore: configurationStore, ConfigurationRollouts: configurationStore,
		Applier: applier, Operations: application.OperationService{Store: operationStore},
		Replicas: replicaSource{replicas: []models.TrafficRolloutReplica{
			{ReplicaID: "candidate-a", Incarnation: "inc-a", ReleaseSHA256: candidateDigest, LeaseExpiresAt: now.Add(time.Minute), Ready: true},
			{ReplicaID: "candidate-b", Incarnation: "inc-b", ReleaseSHA256: candidateDigest, LeaseExpiresAt: now.Add(time.Minute), Ready: true},
			{ReplicaID: "incumbent", Incarnation: "inc-stable", ReleaseSHA256: incumbentDigest, LeaseExpiresAt: now.Add(time.Minute), Ready: true},
		}},
		Validator: configValidator{}, States: application.TrafficRolloutStates{Running: "running", Active: "active", Pending: "pending"},
		OperationStates: application.PluginConfigurationOperationStates{Pending: "pending", Running: "running", Failed: "failed"},
		TargetLostCode:  "target_lost",
		Now:             func() time.Time { return now },
	}
	_, _, err = service.Create(ctx, application.CreateTrafficRolloutCommand{
		ID: "rollout-1", ExpectedRevision: 0, SchemaVersion: 1, Configuration: configuration, Plan: plan,
		Reservation: reservation, Audit: models.AuditRecord{Actor: "operator", Action: "traffic-rollout-create", Resource: "forms", Result: "succeeded", RequestID: "request-1"},
	})
	check(err)
	firstErr := service.ReconcileInstance(ctx, "forms")
	firstPending := firstErr != nil
	service = &application.TrafficRolloutService{
		Store: trafficStore, ConfigurationStore: configurationStore, ConfigurationRollouts: configurationStore,
		Applier: applier, OperationStates: application.PluginConfigurationOperationStates{Pending: "pending", Running: "running", Failed: "failed"},
		TargetLostCode: "target_lost", States: application.TrafficRolloutStates{Completed: "completed"},
	}
	secondErr := service.ReconcileInstance(ctx, "forms")
	check(secondErr)
	targets, found, err := configurationStore.Targets(ctx, "operation-1")
	check(err)
	if !found {
		panic("configuration rollout not found")
	}
	allAcknowledged := len(targets) == 2
	for _, target := range targets {
		allAcknowledged = allAcknowledged && target.Acknowledged
	}
	var barrierClosed bool
	check(database.QueryRowContext(ctx, `SELECT open = 0 FROM plugin_rollouts WHERE operation_id = ?`, "operation-1").Scan(&barrierClosed))
	heldDuringRollout, err := service.ConfigurationCohortHeld(ctx, "forms")
	check(err)
	_, ordinaryConfigErr := configurationStore.CreateCandidate(ctx, "ordinary-operation", "settings", "pending", "forms", 1, 1,
		[]byte(`{"fields":["phone"]}`), models.AuditRecord{Actor: "operator", Action: "settings", Resource: "forms", Result: "succeeded", RequestID: "request-settings"})
	var rolloutConflict models.PluginConfigurationConflict
	ordinarySettingsBlocked := errors.As(ordinaryConfigErr, &rolloutConflict)
	// Re-open the barrier to model a process recovering an unresolved operation
	// whose selected incarnation is now authoritatively lost.
	_, err = database.ExecContext(ctx, `UPDATE plugin_rollouts SET open = 1, completed_at = NULL WHERE operation_id = ?`, "operation-1")
	check(err)
	_, err = database.ExecContext(ctx, `UPDATE plugin_rollout_targets SET acknowledged = 0 WHERE operation_id = ?`, "operation-1")
	check(err)
	_, err = database.ExecContext(ctx, `UPDATE traffic_rollouts SET state = 'running', revision = revision + 1, completed_at = NULL WHERE id = ?`, "rollout-1")
	check(err)
	_, err = database.ExecContext(ctx, `UPDATE operations SET state = 'pending' WHERE id = ?`, "operation-1")
	check(err)
	applier.lost = true
	check(service.ReconcileInstance(ctx, "forms"))
	secondRecord, err := trafficStore.Get(ctx, "rollout-1")
	check(err)
	secondOperation, err := operationStore.Get(ctx, "operation-1")
	check(err)
	heldAfterTargetLost, err := service.ConfigurationCohortHeld(ctx, "forms")
	check(err)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"firstPending": firstPending, "firstCalls": applier.calls[0], "secondCalls": applier.calls[1],
		"candidateTargets":     []string{targets[0].ReplicaID + "/" + targets[0].IncarnationID + ":" + boolText(targets[0].Acknowledged), targets[1].ReplicaID + "/" + targets[1].IncarnationID + ":" + boolText(targets[1].Acknowledged)},
		"incumbentNeverCalled": !containsCall(applier.calls, "incumbent/inc-stable"), "configurationBarrierClosed": barrierClosed && allAcknowledged,
		"lostTargetFencesTrafficRollout":              secondRecord.State == "failed" && secondOperation.State == "failed",
		"ordinarySettingsBlockedDuringTrafficRollout": ordinarySettingsBlocked,
		"configurationCohortHeldDuringRollout":        heldDuringRollout,
		"configurationCohortHeldAfterTargetLost":      heldAfterTargetLost,
	}))
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func containsCall(calls [][]string, expected string) bool {
	for _, call := range calls {
		for _, value := range call {
			if value == expected {
				return true
			}
		}
	}
	return false
}

func boolText(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
