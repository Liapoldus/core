package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func main() {
	ctx := context.Background()
	contract, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, IntegrityCheckQuery: contract.IntegrityCheckQuery,
		ForeignKeyCheckQuery: contract.ForeignKeyCheckQuery, IntegritySuccess: contract.IntegritySuccess,
		IntegrityError: contract.IntegrityError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	check(err)
	defer database.Close()
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances(id, manifest_json, state) VALUES (?, ?, ?)`, "forms", []byte(`{}`), "ready")
	check(err)
	store, err := storage.NewSQLiteTrafficRolloutStore(database)
	check(err)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	settings := []byte(`{ "settings" : [1, 2] }`)
	requestDigest := digest([]byte("request-body"))
	submission := models.TrafficRolloutSubmission{
		ExpectedRevision: 0, SchemaVersion: 1, SettingsJSON: settings,
		Reservation: models.OperationReservation{
			Operation: models.Operation{ID: "operation-1", Kind: "traffic-rollout", State: "pending", CreatedAt: now, RequestID: "request-1", Actor: "operator", Resource: "forms"},
			Scope:     "/api/plugins/forms/rollouts", Key: "rollout-key", RequestDigest: requestDigest,
		},
		Record: models.TrafficRolloutRecord{
			ID: "rollout-1", OperationID: "operation-1", InstanceID: "forms", ReleaseSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			PlanJSON: []byte(`{"releaseSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","targets":[{"replicaId":"candidate-1","incarnation":"inc-candidate"}],"stages":[{"id":"full","candidateWeightPercent":100,"minimumObservationSeconds":0,"requireManualApproval":true}]}`),
			State:    "running", ActiveStageIndex: 0, Revision: 1, CreatedAt: now, UpdatedAt: now,
			Stages:  []models.TrafficRolloutStageRecord{{Index: 0, Stage: models.TrafficRolloutStage{ID: "full", CandidateWeightPercent: 100, RequireManualApproval: true}, State: "active", StartedAt: now}},
			Targets: []models.TrafficRolloutCohortTarget{{Cohort: "candidate", ReplicaID: "candidate-1", IncarnationID: "inc-candidate", ReleaseSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LeaseExpiresAt: now.Add(time.Minute)}, {Cohort: "incumbent", ReplicaID: "stable-1", IncarnationID: "inc-stable", ReleaseSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", LeaseExpiresAt: now.Add(time.Minute)}},
		},
		Audit: models.AuditRecord{Actor: "operator", Action: "traffic_rollout_create", Resource: "forms", Result: "succeeded", RequestID: "request-1"},
	}
	operation, created, err := store.CreateAndPromote(ctx, submission)
	check(err)
	replayed, replayCreated, replayErr := store.CreateAndPromote(ctx, submission)
	check(replayErr)
	different := submission
	different.Reservation.Operation.ID = "operation-2"
	different.Reservation.RequestDigest = digest([]byte("other-request"))
	_, _, differentErr := store.CreateAndPromote(ctx, different)
	var generation int64
	var raw []byte
	check(database.QueryRowContext(ctx, `SELECT generation, raw_json FROM plugin_config_generations WHERE instance_id = ? AND slot = 'active'`, "forms").Scan(&generation, &raw))
	counts := map[string]int{}
	for name, query := range map[string]string{
		"trafficRollouts": `SELECT COUNT(*) FROM traffic_rollouts`, "pluginRollouts": `SELECT COUNT(*) FROM plugin_rollouts`,
		"pluginTargets": `SELECT COUNT(*) FROM plugin_rollout_targets`, "operations": `SELECT COUNT(*) FROM operations`,
		"auditEvents": `SELECT COUNT(*) FROM audit_events`,
	} {
		var count int
		check(database.QueryRowContext(ctx, query).Scan(&count))
		counts[name] = count
	}
	rows, err := database.QueryContext(ctx, `SELECT replica_id FROM plugin_rollout_targets WHERE operation_id = ? ORDER BY replica_id`, operation.ID)
	check(err)
	pluginTargetIDs := make([]string, 0)
	for rows.Next() {
		var replicaID string
		check(rows.Scan(&replicaID))
		pluginTargetIDs = append(pluginTargetIDs, replicaID)
	}
	check(rows.Err())
	check(rows.Close())
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"created": created && operation.ID == "operation-1", "idempotentReplay": !replayCreated && replayed.ID == operation.ID,
		"differentRequestRejected": differentErr != nil, "activeGeneration": generation, "activeRawJSON": string(raw),
		"trafficRollouts": counts["trafficRollouts"], "pluginRollouts": counts["pluginRollouts"],
		"pluginTargets": counts["pluginTargets"], "operations": counts["operations"], "auditEvents": counts["auditEvents"],
		"pluginTargetIDs": pluginTargetIDs,
	}))
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
