package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type replicaSource struct {
	replicas []models.TrafficRolloutReplica
}

func (source replicaSource) TrafficRolloutReplicas(context.Context, string) ([]models.TrafficRolloutReplica, error) {
	return append([]models.TrafficRolloutReplica(nil), source.replicas...), nil
}

type configurationValidator struct{}

func (configurationValidator) ValidateTrafficRolloutConfiguration(_ context.Context, _ string, document []byte) error {
	var value map[string]any
	if json.Unmarshal(document, &value) != nil || value == nil {
		return models.TrafficRolloutInvalid{}
	}
	return nil
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
	rollouts, err := storage.NewSQLiteTrafficRolloutStore(database)
	check(err)
	operations, err := storage.NewSQLiteOperationStore(database)
	check(err)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	configuration := []byte(`{"fields":["name"]}`)
	configurationSchema := []byte(`{"$id":"https://example.test/plugin-config.json","type":"object","required":["fields"],"properties":{"fields":{"type":"array","items":{"type":"string"}}},"additionalProperties":false}`)
	plan := []byte(`{"releaseSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","targets":[{"replicaId":"candidate-1","incarnation":"inc-candidate"}],"stages":[{"id":"canary","candidateWeightPercent":20,"minimumObservationSeconds":60,"requireManualApproval":true},{"id":"full","candidateWeightPercent":100,"minimumObservationSeconds":0}]}`)
	reservation := models.OperationReservation{
		Operation: models.Operation{ID: "operation-1", Kind: "traffic-rollout", State: "pending", CreatedAt: now, RequestID: "request-1", Actor: "operator", Resource: "forms"},
		Scope:     "/api/plugins/forms/rollouts", Key: "rollout-key", RequestDigest: digest([]byte("same request")),
	}
	service := &application.TrafficRolloutService{
		Store: rollouts, Operations: application.OperationService{Store: operations},
		Replicas: replicaSource{replicas: []models.TrafficRolloutReplica{
			{ReplicaID: "candidate-1", Incarnation: "inc-candidate", ReleaseSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LeaseExpiresAt: now.Add(time.Minute), Ready: true},
			{ReplicaID: "stable-1", Incarnation: "inc-stable", ReleaseSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", LeaseExpiresAt: now.Add(time.Minute), Ready: true},
		}},
		Validator: configurationValidator{}, States: application.TrafficRolloutStates{Running: "running", Active: "active", Pending: "pending"},
		Now: func() time.Time { return now },
	}
	command := application.CreateTrafficRolloutCommand{
		ID: "rollout-1", ExpectedRevision: 0, SchemaVersion: 1, Configuration: configuration, Plan: plan,
		Reservation: reservation, Audit: models.AuditRecord{Actor: "operator", Action: "traffic-rollout-create", Resource: "forms", Result: "succeeded", RequestID: "request-1"},
	}
	operation, created, err := service.Create(ctx, command)
	check(err)
	replay, replayCreated, err := service.Create(ctx, command)
	check(err)
	stored, err := rollouts.Get(ctx, "rollout-1")
	check(err)
	wrongReplica := command
	wrongReplica.ID = "rollout-2"
	wrongReplica.Reservation.Operation.ID = "operation-2"
	wrongReplica.Reservation.Key = "other-key"
	wrongReplica.Reservation.RequestDigest = digest([]byte("other request"))
	wrongReplica.Plan = []byte(`{"releaseSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","targets":[{"replicaId":"candidate-1","incarnation":"old-incarnation"}],"stages":[{"id":"full","candidateWeightPercent":100,"minimumObservationSeconds":0}]}`)
	_, _, staleErr := service.Create(ctx, wrongReplica)
	wrongReplica.Plan = []byte(`{"releaseSha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","targets":[{"replicaId":"not-registered","incarnation":"inc-candidate"}],"stages":[{"id":"full","candidateWeightPercent":100,"minimumObservationSeconds":0}]}`)
	_, _, exactErr := service.Create(ctx, wrongReplica)
	check(json.NewEncoder(os.Stdout).Encode(map[string]bool{
		"created": created && operation.ID == "operation-1", "idempotentReplay": !replayCreated && replay.ID == operation.ID,
		"candidateExact":           hasTarget(stored.Targets, "candidate", "candidate-1", "inc-candidate"),
		"incumbentCaptured":        hasTarget(stored.Targets, "incumbent", "stable-1", "inc-stable"),
		"invalidCandidateRejected": exactErr != nil, "staleIncarnationRejected": staleErr != nil,
		"pluginSchemaAccepted": config.ValidateJSONSchemaDocument(configuration, configurationSchema) == nil,
		"pluginSchemaRejected": config.ValidateJSONSchemaDocument([]byte(`{"fields":false}`), configurationSchema) != nil,
	}))
}

func hasTarget(targets []models.TrafficRolloutCohortTarget, cohort, replica, incarnation string) bool {
	for _, target := range targets {
		if target.Cohort == cohort && target.ReplicaID == replica && target.IncarnationID == incarnation {
			return true
		}
	}
	return false
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
