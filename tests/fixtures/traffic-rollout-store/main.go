package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
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
	store, err := storage.NewSQLiteTrafficRolloutStore(database)
	check(err)
	switch os.Args[2] {
	case "create":
		create(ctx, database, store)
	case "recover":
		recoverRecord(ctx, store)
	case "lifecycle":
		lifecycle(ctx, database, store)
	case "confirm-once":
		confirmOnce(ctx, database, store)
	default:
		os.Exit(2)
	}
}

func confirmOnce(ctx context.Context, database *sql.DB, store *storage.SQLiteTrafficRolloutStore) {
	_, err := database.ExecContext(ctx, `INSERT INTO plugin_instances(id, manifest_json, state) VALUES (?, ?, ?)`, "forms", []byte(`{}`), "ready")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO operations(id, kind, state, request_id, actor, resource) VALUES (?, ?, ?, ?, ?, ?)`, "operation-1", "traffic-rollout", "running", "request-1", "operator", "forms")
	check(err)
	rollout := record("operation-1", "rollout-1")
	check(store.Create(ctx, rollout))
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	auditRecord := audit("controller", "traffic_rollout.confirm")
	first, created, firstErr := store.ConfirmStageOnce(ctx, "rollout-1", 1, "canary", 10, "controller-rev-1",
		"spiffe://liapoldus/controller/traffic", "confirm-key", digest("request-one"), base, auditRecord)
	check(firstErr)
	replay, replayed, replayErr := store.ConfirmStageOnce(ctx, "rollout-1", 1, "canary", 10, "controller-rev-1",
		"spiffe://liapoldus/controller/traffic", "confirm-key", digest("request-one"), base.Add(time.Second), auditRecord)
	check(replayErr)
	_, _, conflictErr := store.ConfirmStageOnce(ctx, "rollout-1", 1, "canary", 10, "controller-rev-1",
		"spiffe://liapoldus/controller/traffic", "confirm-key", digest("request-two"), base.Add(2*time.Second), auditRecord)
	var stageState string
	check(database.QueryRowContext(ctx, `SELECT state FROM traffic_rollout_stages WHERE operation_id = ? AND stage_index = 0`, "operation-1").Scan(&stageState))
	var auditEvents int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action = ?`, "traffic_rollout.confirm").Scan(&auditEvents))
	var idempotencyConflict models.IdempotencyConflict
	check(json.NewEncoder(os.Stdout).Encode(map[string]bool{
		"confirmed":                              created && first.State == "awaiting_manual_approval",
		"exactReplayReturnsOriginalReceipt":      !replayed && replay == first,
		"reusedKeyWithDifferentRequestConflicts": errors.As(conflictErr, &idempotencyConflict),
		"stageChangedOnce":                       stageState == "confirmed",
		"auditWrittenOnce":                       auditEvents == 1,
	}))
}

func digest(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func lifecycle(ctx context.Context, database *sql.DB, store *storage.SQLiteTrafficRolloutStore) {
	_, err := database.ExecContext(ctx, `INSERT INTO plugin_instances(id, manifest_json, state) VALUES (?, ?, ?)`, "forms", []byte(`{}`), "ready")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO operations(id, kind, state, request_id, actor, resource) VALUES (?, ?, ?, ?, ?, ?)`, "operation-1", "traffic-rollout", "running", "request-1", "operator", "forms")
	check(err)
	first := record("operation-1", "rollout-1")
	first.Stages[0].Stage.RequireManualApproval = true
	first.Stages[1].Stage.RequireManualApproval = true
	first.Stages[0].Stage.MinimumObservationSeconds = 60
	first.PlanJSON = planDocument(true)
	check(store.Create(ctx, first))
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	_, wrongWeightErr := store.ConfirmStage(ctx, "rollout-1", 1, "canary", 9, "controller-1", base, audit("controller", "confirm"))
	_, confirmationErr := store.ConfirmStage(ctx, "rollout-1", 1, "canary", 10, "controller-1", base, audit("controller", "confirm"))
	check(confirmationErr)
	_, prematureErr := store.ApproveStage(ctx, "rollout-1", 2, "canary", "platform-admin", base.Add(59*time.Second), audit("platform-admin", "approve"))
	_, staleErr := store.ApproveStage(ctx, "rollout-1", 1, "canary", "platform-admin", base.Add(time.Minute), audit("platform-admin", "approve"))
	advanced, approvalErr := store.ApproveStage(ctx, "rollout-1", 2, "canary", "platform-admin", base.Add(time.Minute), audit("platform-admin", "approve"))
	check(approvalErr)
	_, finalConfirmErr := store.ConfirmStage(ctx, "rollout-1", 3, "full", 100, "controller-2", base.Add(2*time.Minute), audit("controller", "confirm"))
	check(finalConfirmErr)
	completed, finalApprovalErr := store.ApproveStage(ctx, "rollout-1", 4, "full", "platform-admin", base.Add(2*time.Minute), audit("platform-admin", "approve"))
	check(finalApprovalErr)
	var auditEvents int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE resource = ?`, "rollout-1").Scan(&auditEvents))
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"wrongWeightRejected":       wrongWeightErr != nil,
		"prematureApprovalRejected": prematureErr != nil,
		"staleRevisionRejected":     staleErr != nil,
		"firstApprovalAdvanced":     approvalErr == nil && advanced.ActiveStageIndex == 1,
		"finalApprovalCompleted":    finalApprovalErr == nil && completed.State == "completed",
		"activeStageIndex":          advanced.ActiveStageIndex,
		"finalState":                completed.State,
		"auditEvents":               auditEvents,
	}))
}

func audit(actor, action string) models.AuditRecord {
	return models.AuditRecord{Actor: actor, Action: action, Resource: "rollout-1", Result: "succeeded", RequestID: action + "-request"}
}

func planDocument(requireApproval bool) []byte {
	approval := "false"
	if requireApproval {
		approval = "true"
	}
	return []byte(`{ "releaseSha256" : "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "targets" : [{"replicaId":"candidate-1","incarnation":"inc-candidate"}], "stages" : [{"id":"canary","candidateWeightPercent":10,"minimumObservationSeconds":60,"requireManualApproval":` + approval + `},{"id":"full","candidateWeightPercent":100,"minimumObservationSeconds":0,"requireManualApproval":` + approval + `}] }`)
}

func create(ctx context.Context, database *sql.DB, store *storage.SQLiteTrafficRolloutStore) {
	// The fixture inserts only the control-plane parent rows required by the
	// durable traffic rollout aggregate.
	_, err := database.ExecContext(ctx, `INSERT INTO plugin_instances(id, manifest_json, state) VALUES (?, ?, ?)`, "forms", []byte(`{}`), "ready")
	check(err)
	for _, id := range []string{"operation-1", "operation-2"} {
		_, err = database.ExecContext(ctx, `INSERT INTO operations(id, kind, state, request_id, actor, resource) VALUES (?, ?, ?, ?, ?, ?)`, id, "traffic-rollout", "running", id, "operator", "forms")
		check(err)
	}
	if err := store.Create(ctx, record("operation-1", "rollout-1")); err != nil {
		panic(err)
	}
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_rollouts(operation_id, instance_id, generation, open, created_at) VALUES (?, ?, ?, 0, ?)`,
		"operation-1", "forms", 8, time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano))
	check(err)
	duplicateRejected := store.Create(ctx, record("operation-2", "rollout-2")) == (models.TrafficRolloutConflict{})
	check(json.NewEncoder(os.Stdout).Encode(map[string]bool{"duplicateRejected": duplicateRejected}))
}

func recoverRecord(ctx context.Context, store *storage.SQLiteTrafficRolloutStore) {
	loaded, err := store.Get(ctx, "rollout-1")
	check(err)
	loaded.PlanJSON = append([]byte(nil), loaded.PlanJSON...)
	out := struct {
		ID               string                              `json:"ID"`
		InstanceID       string                              `json:"InstanceID"`
		Generation       int64                               `json:"Generation"`
		State            string                              `json:"State"`
		ActiveStageIndex int                                 `json:"ActiveStageIndex"`
		Revision         int64                               `json:"Revision"`
		Stages           []models.TrafficRolloutStageRecord  `json:"Stages"`
		Targets          []models.TrafficRolloutCohortTarget `json:"Targets"`
		PlanJSON         string                              `json:"PlanJSON"`
	}{loaded.ID, loaded.InstanceID, loaded.Generation, loaded.State, loaded.ActiveStageIndex, loaded.Revision, loaded.Stages, loaded.Targets, string(loaded.PlanJSON)}
	check(json.NewEncoder(os.Stdout).Encode(out))
}

func record(operationID, id string) models.TrafficRolloutRecord {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	return models.TrafficRolloutRecord{
		ID: id, OperationID: operationID, InstanceID: "forms", Generation: 8,
		ReleaseSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PlanJSON:      planDocument(true), State: "running", ActiveStageIndex: 0,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
		Stages: []models.TrafficRolloutStageRecord{
			{Index: 0, Stage: models.TrafficRolloutStage{ID: "canary", CandidateWeightPercent: 10, MinimumObservationSeconds: 60, RequireManualApproval: true}, State: "active", StartedAt: now},
			{Index: 1, Stage: models.TrafficRolloutStage{ID: "full", CandidateWeightPercent: 100, RequireManualApproval: true}, State: "pending"},
		},
		Targets: []models.TrafficRolloutCohortTarget{
			{Cohort: "candidate", ReplicaID: "candidate-1", IncarnationID: "inc-candidate", ReleaseSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LeaseExpiresAt: now.Add(time.Minute)},
			{Cohort: "incumbent", ReplicaID: "stable-1", IncarnationID: "inc-stable", ReleaseSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", LeaseExpiresAt: now.Add(time.Minute)},
		},
	}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
