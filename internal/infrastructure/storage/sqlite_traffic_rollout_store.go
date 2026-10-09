package storage

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type SQLiteTrafficRolloutStore struct {
	database        *sql.DB
	queries         map[string]string
	timestampLayout string
	configuration   pluginConfigurationStoreContract
	operation       *SQLiteOperationStore
	idempotencyTill time.Time
	audit           *SQLiteAuditStore
}

type trafficRolloutStoreContract struct {
	TimestampLayout string
}

var _ interfaces.TrafficRolloutStore = (*SQLiteTrafficRolloutStore)(nil)

func (store *SQLiteTrafficRolloutStore) CreateAndPromote(ctx context.Context, submission models.TrafficRolloutSubmission) (models.Operation, bool, error) {
	if store == nil || store.database == nil || store.operation == nil || store.audit == nil {
		return models.Operation{}, false, models.TrafficRolloutInvalid{}
	}
	reservation := submission.Reservation
	operation := reservation.Operation
	if !validOperation(operation) || reservation.Scope == "" || reservation.Key == "" || reservation.RequestDigest == "" ||
		reservation.Payload != nil || submission.ExpectedRevision < 0 || submission.SchemaVersion < 1 ||
		len(submission.SettingsJSON) > store.configuration.MaximumPayloadSize || !validPluginConfiguration(submission.SettingsJSON) ||
		submission.Record.OperationID != operation.ID || submission.Record.InstanceID != operation.Resource ||
		!validConfigurationAudit(submission.Audit) || submission.Audit.Actor != operation.Actor ||
		submission.Audit.Resource != operation.Resource || submission.Audit.RequestID != operation.RequestID {
		return models.Operation{}, false, models.TrafficRolloutInvalid{}
	}
	if len(reservation.RequestDigest) != 64 || !validSHA256(reservation.RequestDigest) {
		return models.Operation{}, false, models.TrafficRolloutInvalid{}
	}
	now := time.Now().UTC()
	if now.Before(operation.CreatedAt.Add(-time.Minute)) {
		return models.Operation{}, false, models.TrafficRolloutInvalid{}
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.Operation{}, false, err
	}
	defer tx.Rollback()
	keyDigest := idempotencyKeyDigest(reservation.Key)
	if _, err := tx.ExecContext(ctx, store.operation.queries["delete-expired-idempotency"], operation.Actor,
		reservation.Scope, keyDigest, now.Format(store.timestampLayout)); err != nil {
		return models.Operation{}, false, err
	}
	existing, found, err := findReservedOperation(ctx, tx, store.operation, operation.Actor, reservation.Scope,
		keyDigest, reservation.RequestDigest, now.Format(store.timestampLayout))
	if err != nil {
		return models.Operation{}, false, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return models.Operation{}, false, err
		}
		return existing, false, nil
	}
	var instanceExists bool
	if err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-instance-exists"], operation.Resource).Scan(&instanceExists); err != nil {
		return models.Operation{}, false, err
	}
	if !instanceExists {
		return models.Operation{}, false, models.PluginConfigurationNotFound{}
	}
	var activeGeneration int64
	var activeDigest string
	if err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-active-generation"], operation.Resource,
		store.configuration.Slots.Active, operation.Resource, store.configuration.Slots.Active).Scan(&activeGeneration, &activeDigest); err != nil {
		return models.Operation{}, false, err
	}
	if activeGeneration != submission.ExpectedRevision {
		return models.Operation{}, false, models.PluginConfigurationConflict{}
	}
	var stagingCount int
	if err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-staging-count"], operation.Resource, store.configuration.Slots.Staging).Scan(&stagingCount); err != nil {
		return models.Operation{}, false, err
	}
	if stagingCount != 0 {
		return models.Operation{}, false, models.PluginConfigurationConflict{}
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-next-generation"], operation.Resource).Scan(&generation); err != nil {
		return models.Operation{}, false, err
	}
	if _, err := tx.ExecContext(ctx, store.operation.queries["insert-operation"], operationValues(operation, store.timestampLayout)...); err != nil {
		if isUniqueConstraint(err) {
			return models.Operation{}, false, models.TrafficRolloutConflict{}
		}
		return models.Operation{}, false, err
	}
	if _, err := tx.ExecContext(ctx, store.operation.queries["insert-idempotency"], operation.Actor, reservation.Scope,
		keyDigest, reservation.RequestDigest, operation.ID, now.Format(store.timestampLayout),
		store.idempotencyTill.UTC().Format(store.timestampLayout)); err != nil {
		if isUniqueConstraint(err) {
			return models.Operation{}, false, models.TrafficRolloutConflict{}
		}
		return models.Operation{}, false, err
	}
	createdAt := now.Format(store.timestampLayout)
	configDigest := configurationDigest(submission.SettingsJSON)
	if _, err := tx.ExecContext(ctx, store.queries["traffic-rollout-insert-generation"], operation.Resource, generation,
		store.configuration.Slots.Staging, submission.SettingsJSON, configDigest, submission.SchemaVersion, createdAt); err != nil {
		if isUniqueConstraint(err) {
			return models.Operation{}, false, models.PluginConfigurationConflict{}
		}
		return models.Operation{}, false, err
	}
	record := submission.Record
	record.Generation = generation
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if !validTrafficRolloutRecord(record) {
		return models.Operation{}, false, models.TrafficRolloutInvalid{}
	}
	if _, err := tx.ExecContext(ctx, store.queries["traffic-rollout-plugin-rollout-create"], operation.ID,
		record.InstanceID, generation, createdAt); err != nil {
		if isUniqueConstraint(err) {
			return models.Operation{}, false, models.TrafficRolloutConflict{}
		}
		return models.Operation{}, false, err
	}
	for _, target := range record.Targets {
		// Only the candidate cohort receives the newly promoted configuration.
		// Incumbents keep their current in-memory generation throughout the
		// canary; their traffic membership is persisted separately above.
		if target.Cohort != "candidate" {
			continue
		}
		if _, err := tx.ExecContext(ctx, store.queries["traffic-rollout-plugin-target-create"], operation.ID,
			target.ReplicaID, target.IncarnationID, target.ReleaseSHA256); err != nil {
			return models.Operation{}, false, err
		}
		if _, err := tx.ExecContext(ctx, store.queries["traffic-rollout-plugin-target-lease-create"], operation.ID,
			target.ReplicaID, target.LeaseExpiresAt.UTC().Format(store.timestampLayout)); err != nil {
			return models.Operation{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, store.queries["traffic-rollout-delete-previous"], operation.Resource, store.configuration.Slots.Previous); err != nil {
		return models.Operation{}, false, err
	}
	if activeGeneration > 0 {
		if _, err := tx.ExecContext(ctx, store.queries["traffic-rollout-move-generation-slot"], store.configuration.Slots.Previous,
			operation.Resource, activeGeneration, store.configuration.Slots.Active); err != nil {
			return models.Operation{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, store.queries["traffic-rollout-move-generation-slot"], store.configuration.Slots.Active,
		operation.Resource, generation, store.configuration.Slots.Staging); err != nil {
		return models.Operation{}, false, err
	}
	submission.Audit.Timestamp = now
	submission.Audit.DigestBefore = activeDigest
	submission.Audit.DigestAfter = configDigest
	if err := store.audit.append(ctx, tx, submission.Audit); err != nil {
		return models.Operation{}, false, err
	}
	if err := insertTrafficRollout(ctx, tx, store, record); err != nil {
		return models.Operation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return models.Operation{}, false, err
	}
	return operation, true, nil
}

func NewSQLiteTrafficRolloutStore(database *sql.DB) (*SQLiteTrafficRolloutStore, error) {
	operationContract := operationDefinitions()
	contract := trafficRolloutStoreContract{TimestampLayout: operationContract.TimestampLayout}
	configuration := ConfigurationDefinitions()
	if database == nil {
		return nil, models.TrafficRolloutInvalid{}
	}
	idempotencyTill, err := time.Parse(operationContract.TimestampLayout, operationContract.IdempotencyExpiresAt)
	if err != nil {
		return nil, models.TrafficRolloutInvalid{}
	}
	queries := trafficRolloutQueries()
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	operation, err := NewSQLiteOperationStore(database)
	if err != nil {
		return nil, err
	}
	return &SQLiteTrafficRolloutStore{
		database: database, queries: queries, timestampLayout: contract.TimestampLayout,
		configuration: configuration, operation: operation, idempotencyTill: idempotencyTill, audit: audit,
	}, nil
}

func (store *SQLiteTrafficRolloutStore) ConfirmStage(
	ctx context.Context,
	id string,
	expectedRevision int64,
	stageID string,
	appliedWeight int,
	controllerRevision string,
	at time.Time,
	audit models.AuditRecord,
) (models.TrafficRolloutRecord, error) {
	_, _, err := store.confirmStage(ctx, id, expectedRevision, stageID, appliedWeight, controllerRevision, "", "", "", at, audit)
	if err != nil {
		return models.TrafficRolloutRecord{}, err
	}
	return store.Get(ctx, id)
}

func (store *SQLiteTrafficRolloutStore) ConfirmStageOnce(
	ctx context.Context,
	id string,
	expectedRevision int64,
	stageID string,
	appliedWeight int,
	controllerRevision string,
	controllerIdentity string,
	idempotencyKey string,
	requestDigest string,
	at time.Time,
	audit models.AuditRecord,
) (models.TrafficRolloutConfirmationReceipt, bool, error) {
	if controllerIdentity == "" || idempotencyKey == "" || !validSHA256(requestDigest) {
		return models.TrafficRolloutConfirmationReceipt{}, false, models.TrafficRolloutInvalid{}
	}
	return store.confirmStage(ctx, id, expectedRevision, stageID, appliedWeight, controllerRevision,
		controllerIdentity, idempotencyKey, requestDigest, at, audit)
}

func (store *SQLiteTrafficRolloutStore) confirmStage(
	ctx context.Context,
	id string,
	expectedRevision int64,
	stageID string,
	appliedWeight int,
	controllerRevision string,
	controllerIdentity string,
	idempotencyKey string,
	requestDigest string,
	at time.Time,
	audit models.AuditRecord,
) (models.TrafficRolloutConfirmationReceipt, bool, error) {
	if store == nil || store.database == nil || id == "" || expectedRevision < 1 || stageID == "" ||
		appliedWeight < 1 || appliedWeight > 100 || controllerRevision == "" || at.IsZero() || !validTrafficRolloutAudit(audit) {
		return models.TrafficRolloutConfirmationReceipt{}, false, models.TrafficRolloutInvalid{}
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.TrafficRolloutConfirmationReceipt{}, false, err
	}
	defer tx.Rollback()
	if idempotencyKey != "" {
		keyDigest := idempotencyKeyDigest(idempotencyKey)
		var existingDigest string
		var encoded []byte
		err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-confirmation-select"], controllerIdentity, id, keyDigest).Scan(&existingDigest, &encoded)
		if err == nil {
			if existingDigest != requestDigest {
				return models.TrafficRolloutConfirmationReceipt{}, false, models.IdempotencyConflict{}
			}
			var receipt models.TrafficRolloutConfirmationReceipt
			if json.Unmarshal(encoded, &receipt) != nil || receipt.RolloutID != id || receipt.StageID == "" {
				return models.TrafficRolloutConfirmationReceipt{}, false, models.TrafficRolloutInvalid{}
			}
			if err := tx.Commit(); err != nil {
				return models.TrafficRolloutConfirmationReceipt{}, false, err
			}
			return receipt, false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return models.TrafficRolloutConfirmationReceipt{}, false, err
		}
	}
	state, err := trafficRolloutState(ctx, tx, store, id)
	if err != nil {
		return models.TrafficRolloutConfirmationReceipt{}, false, err
	}
	if state.state != "running" || state.revision != expectedRevision {
		return models.TrafficRolloutConfirmationReceipt{}, false, models.TrafficRolloutConflict{}
	}
	stage, err := trafficRolloutStage(ctx, tx, store, state.operationID, state.activeStageIndex)
	if err != nil {
		return models.TrafficRolloutConfirmationReceipt{}, false, err
	}
	if stage.id != stageID || stage.state != "active" || stage.weight != appliedWeight {
		return models.TrafficRolloutConfirmationReceipt{}, false, models.TrafficRolloutConflict{}
	}
	updated, err := tx.ExecContext(ctx, store.queries["traffic-rollout-confirm-stage"],
		at.UTC().Format(store.timestampLayout), appliedWeight, controllerRevision,
		state.operationID, state.activeStageIndex, stageID, appliedWeight)
	if err != nil {
		return models.TrafficRolloutConfirmationReceipt{}, false, err
	}
	if err := requireOneRow(updated); err != nil {
		return models.TrafficRolloutConfirmationReceipt{}, false, models.TrafficRolloutConflict{}
	}
	if err := updateTrafficRolloutRevision(ctx, tx, store, id, state, "running", state.activeStageIndex, controllerRevision, at, time.Time{}); err != nil {
		return models.TrafficRolloutConfirmationReceipt{}, false, err
	}
	if state.activeStageIndex == 0 {
		started, err := tx.ExecContext(ctx, store.queries["traffic-rollout-start-operation"], at.UTC().Format(store.timestampLayout), state.operationID)
		if err != nil {
			return models.TrafficRolloutConfirmationReceipt{}, false, err
		}
		changed, err := started.RowsAffected()
		if err != nil {
			return models.TrafficRolloutConfirmationReceipt{}, false, err
		}
		if changed == 0 {
			var operationState string
			if err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-operation-state"], state.operationID).Scan(&operationState); err != nil || operationState != "running" {
				return models.TrafficRolloutConfirmationReceipt{}, false, models.TrafficRolloutConflict{}
			}
		}
	}
	receipt := models.TrafficRolloutConfirmationReceipt{RolloutID: id, Revision: expectedRevision + 1,
		StageID: stageID, AppliedWeight: appliedWeight, ControllerRevision: controllerRevision, State: "awaiting_manual_approval"}
	if idempotencyKey != "" {
		encoded, err := json.Marshal(receipt)
		if err != nil {
			return models.TrafficRolloutConfirmationReceipt{}, false, err
		}
		if _, err := tx.ExecContext(ctx, store.queries["traffic-rollout-confirmation-insert"], controllerIdentity, id,
			idempotencyKeyDigest(idempotencyKey), requestDigest, encoded, at.UTC().Format(store.timestampLayout)); err != nil {
			if isUniqueConstraint(err) {
				return models.TrafficRolloutConfirmationReceipt{}, false, models.TrafficRolloutConflict{}
			}
			return models.TrafficRolloutConfirmationReceipt{}, false, err
		}
	}
	audit.Timestamp = at.UTC()
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return models.TrafficRolloutConfirmationReceipt{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return models.TrafficRolloutConfirmationReceipt{}, false, err
	}
	return receipt, true, nil
}

func (store *SQLiteTrafficRolloutStore) ApproveStage(
	ctx context.Context,
	id string,
	expectedRevision int64,
	stageID string,
	actor string,
	at time.Time,
	audit models.AuditRecord,
) (models.TrafficRolloutRecord, error) {
	if store == nil || store.database == nil || id == "" || expectedRevision < 1 || stageID == "" ||
		actor == "" || at.IsZero() || !validTrafficRolloutAudit(audit) || audit.Actor != actor {
		return models.TrafficRolloutRecord{}, models.TrafficRolloutInvalid{}
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.TrafficRolloutRecord{}, err
	}
	defer tx.Rollback()
	if err := approveTrafficRolloutStage(ctx, tx, store, id, expectedRevision, stageID, actor, at, audit); err != nil {
		return models.TrafficRolloutRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.TrafficRolloutRecord{}, err
	}
	return store.Get(ctx, id)
}

func (store *SQLiteTrafficRolloutStore) ApproveStageWithOperation(
	ctx context.Context,
	reservation models.OperationReservation,
	id string,
	expectedRevision int64,
	stageID string,
	at time.Time,
	audit models.AuditRecord,
) (models.Operation, bool, error) {
	operation := reservation.Operation
	if store == nil || store.database == nil || id == "" || expectedRevision < 1 || stageID == "" || at.IsZero() ||
		!validOperation(operation) || operation.Resource != id || reservation.Scope == "" || reservation.Key == "" ||
		reservation.Payload != nil || !validSHA256(reservation.RequestDigest) || !validTrafficRolloutAudit(audit) || audit.Actor != operation.Actor {
		return models.Operation{}, false, models.TrafficRolloutInvalid{}
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.Operation{}, false, err
	}
	defer tx.Rollback()
	now := at.UTC()
	keyDigest := idempotencyKeyDigest(reservation.Key)
	if _, err := tx.ExecContext(ctx, store.operation.queries["delete-expired-idempotency"], operation.Actor,
		reservation.Scope, keyDigest, now.Format(store.timestampLayout)); err != nil {
		return models.Operation{}, false, err
	}
	existing, found, err := findReservedOperation(ctx, tx, store.operation, operation.Actor, reservation.Scope,
		keyDigest, reservation.RequestDigest, now.Format(store.timestampLayout))
	if err != nil {
		return models.Operation{}, false, err
	}
	if found {
		if err := tx.Commit(); err != nil {
			return models.Operation{}, false, err
		}
		return existing, false, nil
	}
	operation.CreatedAt = now
	if _, err := tx.ExecContext(ctx, store.operation.queries["insert-operation"], operationValues(operation, store.timestampLayout)...); err != nil {
		if isUniqueConstraint(err) {
			return models.Operation{}, false, models.TrafficRolloutConflict{}
		}
		return models.Operation{}, false, err
	}
	if _, err := tx.ExecContext(ctx, store.operation.queries["insert-idempotency"], operation.Actor, reservation.Scope,
		keyDigest, reservation.RequestDigest, operation.ID, now.Format(store.timestampLayout),
		store.idempotencyTill.UTC().Format(store.timestampLayout)); err != nil {
		if isUniqueConstraint(err) {
			return models.Operation{}, false, models.TrafficRolloutConflict{}
		}
		return models.Operation{}, false, err
	}
	if err := approveTrafficRolloutStage(ctx, tx, store, id, expectedRevision, stageID, operation.Actor, now, audit); err != nil {
		return models.Operation{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return models.Operation{}, false, err
	}
	return operation, true, nil
}

func approveTrafficRolloutStage(
	ctx context.Context,
	tx *sql.Tx,
	store *SQLiteTrafficRolloutStore,
	id string,
	expectedRevision int64,
	stageID string,
	actor string,
	at time.Time,
	audit models.AuditRecord,
) error {
	state, err := trafficRolloutState(ctx, tx, store, id)
	if err != nil {
		return err
	}
	if state.state != "running" || state.revision != expectedRevision {
		return models.TrafficRolloutConflict{}
	}
	stage, err := trafficRolloutStage(ctx, tx, store, state.operationID, state.activeStageIndex)
	if err != nil {
		return err
	}
	if stage.id != stageID || stage.state != "confirmed" || stage.appliedWeight != stage.weight || stage.confirmedAt.IsZero() ||
		stage.requiresApproval != 1 || at.Before(stage.confirmedAt.Add(time.Duration(stage.minimumObservationSeconds)*time.Second)) {
		return models.TrafficRolloutConflict{}
	}
	var stageCount int
	if err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-stage-count"], state.operationID).Scan(&stageCount); err != nil {
		return err
	}
	now := at.UTC().Format(store.timestampLayout)
	updated, err := tx.ExecContext(ctx, store.queries["traffic-rollout-update-stage"], "approved", nil, actor, now,
		state.operationID, state.activeStageIndex, stageID, "confirmed")
	if err != nil {
		return err
	}
	if err := requireOneRow(updated); err != nil {
		return models.TrafficRolloutConflict{}
	}
	finished := state.activeStageIndex+1 == stageCount
	nextIndex := state.activeStageIndex
	nextState := "running"
	var completedAt time.Time
	if finished {
		if stage.weight != 100 {
			return models.TrafficRolloutConflict{}
		}
		nextState = "completed"
		completedAt = at.UTC()
	} else {
		nextIndex++
		var nextStageID string
		if err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-next-stage-id"], state.operationID, nextIndex).Scan(&nextStageID); err != nil {
			return models.TrafficRolloutConflict{}
		}
		activated, err := tx.ExecContext(ctx, store.queries["traffic-rollout-update-stage"], "active", now, nil, nil,
			state.operationID, nextIndex, nextStageID, "pending")
		if err != nil {
			return err
		}
		if err := requireOneRow(activated); err != nil {
			return models.TrafficRolloutConflict{}
		}
	}
	if err := updateTrafficRolloutRevision(ctx, tx, store, id, state, nextState, nextIndex, "", at, completedAt); err != nil {
		return err
	}
	if finished {
		result, err := tx.ExecContext(ctx, store.queries["traffic-rollout-complete-operation"], now, state.operationID)
		if err != nil {
			return err
		}
		if err := requireOneRow(result); err != nil {
			return models.TrafficRolloutConflict{}
		}
	}
	audit.Timestamp = at.UTC()
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return err
	}
	return nil
}

func (store *SQLiteTrafficRolloutStore) Create(ctx context.Context, record models.TrafficRolloutRecord) error {
	if store == nil || store.database == nil {
		return models.TrafficRolloutInvalid{}
	}
	if !validTrafficRolloutRecord(record) {
		return models.TrafficRolloutInvalid{}
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertTrafficRollout(ctx, tx, store, record); err != nil {
		return err
	}
	return tx.Commit()
}

func insertTrafficRollout(ctx context.Context, tx *sql.Tx, store *SQLiteTrafficRolloutStore, record models.TrafficRolloutRecord) error {
	if !validTrafficRolloutRecord(record) {
		return models.TrafficRolloutInvalid{}
	}
	createdAt := record.CreatedAt.UTC()
	updatedAt := record.UpdatedAt.UTC()
	if createdAt.IsZero() || updatedAt.IsZero() {
		return models.TrafficRolloutInvalid{}
	}
	var completedAt any
	if !record.CompletedAt.IsZero() {
		completedAt = record.CompletedAt.UTC().Format(store.timestampLayout)
	}
	_, err := tx.ExecContext(ctx, store.queries["traffic-rollout-create"], record.ID, record.OperationID,
		record.InstanceID, record.Generation, record.ReleaseSHA256, record.PlanJSON, record.State,
		record.ActiveStageIndex, nullableString(record.ControllerRevision), record.Revision,
		createdAt.Format(store.timestampLayout), updatedAt.Format(store.timestampLayout), completedAt)
	if err != nil {
		if isUniqueConstraint(err) {
			return models.TrafficRolloutConflict{}
		}
		return err
	}
	for _, stage := range record.Stages {
		_, err := tx.ExecContext(ctx, store.queries["traffic-rollout-stage-create"], record.OperationID,
			stage.Index, stage.Stage.ID, stage.Stage.CandidateWeightPercent, stage.Stage.MinimumObservationSeconds,
			boolInt(stage.Stage.RequireManualApproval), stage.State, nullableTime(stage.StartedAt, store.timestampLayout),
			nullableTime(stage.ConfirmedAt, store.timestampLayout), nullableAppliedWeight(stage.AppliedCandidateWeight),
			nullableString(stage.ControllerRevision), nullableString(stage.ApprovedBy), nullableTime(stage.ApprovedAt, store.timestampLayout))
		if err != nil {
			if isUniqueConstraint(err) {
				return models.TrafficRolloutConflict{}
			}
			return err
		}
	}
	for _, target := range record.Targets {
		_, err := tx.ExecContext(ctx, store.queries["traffic-rollout-target-create"], record.OperationID,
			target.Cohort, target.ReplicaID, target.IncarnationID, target.ReleaseSHA256,
			target.LeaseExpiresAt.UTC().Format(store.timestampLayout))
		if err != nil {
			if isUniqueConstraint(err) {
				return models.TrafficRolloutConflict{}
			}
			return err
		}
	}
	return nil
}

func (store *SQLiteTrafficRolloutStore) Get(ctx context.Context, id string) (models.TrafficRolloutRecord, error) {
	if store == nil || store.database == nil || id == "" {
		return models.TrafficRolloutRecord{}, models.TrafficRolloutInvalid{}
	}
	var record models.TrafficRolloutRecord
	var createdAt, updatedAt, completedAt string
	err := store.database.QueryRowContext(ctx, store.queries["traffic-rollout-select"], id).Scan(
		&record.ID, &record.OperationID, &record.InstanceID, &record.Generation, &record.ReleaseSHA256,
		&record.PlanJSON, &record.State, &record.ActiveStageIndex, &record.ControllerRevision, &record.Revision,
		&createdAt, &updatedAt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.TrafficRolloutRecord{}, models.TrafficRolloutNotFound{}
	}
	if err != nil {
		return models.TrafficRolloutRecord{}, err
	}
	if record.CreatedAt, err = time.Parse(store.timestampLayout, createdAt); err != nil {
		return models.TrafficRolloutRecord{}, err
	}
	if record.UpdatedAt, err = time.Parse(store.timestampLayout, updatedAt); err != nil {
		return models.TrafficRolloutRecord{}, err
	}
	if completedAt != "" {
		if record.CompletedAt, err = time.Parse(store.timestampLayout, completedAt); err != nil {
			return models.TrafficRolloutRecord{}, err
		}
	}
	if record.Stages, err = store.readStages(ctx, record.OperationID); err != nil {
		return models.TrafficRolloutRecord{}, err
	}
	if record.Targets, err = store.readTargets(ctx, record.OperationID); err != nil {
		return models.TrafficRolloutRecord{}, err
	}
	return record, nil
}

// OpenConfigurationRollouts returns durable candidate-generation barriers for
// one plugin instance. It is used both after create and during process recovery.
func (store *SQLiteTrafficRolloutStore) OpenConfigurationRollouts(ctx context.Context, instanceID string) ([]models.PluginConfigurationRollout, error) {
	if store == nil || store.database == nil || instanceID == "" {
		return nil, models.TrafficRolloutInvalid{}
	}
	rows, err := store.database.QueryContext(ctx, store.queries["traffic-rollout-open-configurations"], instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rollouts := make([]models.PluginConfigurationRollout, 0)
	for rows.Next() {
		var rollout models.PluginConfigurationRollout
		if err := rows.Scan(&rollout.OperationID, &rollout.InstanceID, &rollout.Generation); err != nil {
			return nil, err
		}
		if rollout.OperationID == "" || rollout.InstanceID != instanceID || rollout.Generation < 1 {
			return nil, models.TrafficRolloutInvalid{}
		}
		rollouts = append(rollouts, rollout)
	}
	return rollouts, rows.Err()
}

// ConfigurationCohortHeld fences generic active-generation fanout while the
// latest release rollout is running or failed. Only an explicitly completed
// rollout releases incumbents back to ordinary configuration reconciliation.
func (store *SQLiteTrafficRolloutStore) ConfigurationCohortHeld(ctx context.Context, instanceID, completedState string) (bool, error) {
	if store == nil || store.database == nil || instanceID == "" || completedState == "" {
		return false, models.TrafficRolloutInvalid{}
	}
	var state string
	err := store.database.QueryRowContext(ctx, store.queries["traffic-rollout-latest-state"], instanceID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || state == "" {
		return false, models.TrafficRolloutInvalid{}
	}
	return state != completedState, nil
}

func (store *SQLiteTrafficRolloutStore) readStages(ctx context.Context, operationID string) ([]models.TrafficRolloutStageRecord, error) {
	rows, err := store.database.QueryContext(ctx, store.queries["traffic-rollout-stages-select"], operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stages := make([]models.TrafficRolloutStageRecord, 0)
	for rows.Next() {
		var stage models.TrafficRolloutStageRecord
		var startedAt, confirmedAt, approvedAt string
		var requiredApproval int
		if err := rows.Scan(&stage.Index, &stage.Stage.ID, &stage.Stage.CandidateWeightPercent,
			&stage.Stage.MinimumObservationSeconds, &requiredApproval, &stage.State, &startedAt,
			&confirmedAt, &stage.AppliedCandidateWeight, &stage.ControllerRevision, &stage.ApprovedBy, &approvedAt); err != nil {
			return nil, err
		}
		stage.Stage.RequireManualApproval = requiredApproval == 1
		if stage.StartedAt, err = parseOptionalTime(store.timestampLayout, startedAt); err != nil {
			return nil, err
		}
		if stage.ConfirmedAt, err = parseOptionalTime(store.timestampLayout, confirmedAt); err != nil {
			return nil, err
		}
		if stage.ApprovedAt, err = parseOptionalTime(store.timestampLayout, approvedAt); err != nil {
			return nil, err
		}
		if stage.AppliedCandidateWeight < 0 {
			stage.AppliedCandidateWeight = 0
		}
		stages = append(stages, stage)
	}
	return stages, rows.Err()
}

func (store *SQLiteTrafficRolloutStore) readTargets(ctx context.Context, operationID string) ([]models.TrafficRolloutCohortTarget, error) {
	rows, err := store.database.QueryContext(ctx, store.queries["traffic-rollout-targets-select"], operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]models.TrafficRolloutCohortTarget, 0)
	for rows.Next() {
		var target models.TrafficRolloutCohortTarget
		var expiresAt string
		if err := rows.Scan(&target.Cohort, &target.ReplicaID, &target.IncarnationID, &target.ReleaseSHA256, &expiresAt); err != nil {
			return nil, err
		}
		if target.LeaseExpiresAt, err = time.Parse(store.timestampLayout, expiresAt); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func validTrafficRolloutRecord(record models.TrafficRolloutRecord) bool {
	if record.ID == "" || record.OperationID == "" || record.InstanceID == "" || record.Generation < 1 ||
		!validSHA256(record.ReleaseSHA256) || len(record.PlanJSON) == 0 || !json.Valid(record.PlanJSON) ||
		record.State != "running" || record.ActiveStageIndex != 0 || record.Revision < 1 || len(record.Stages) == 0 || len(record.Targets) == 0 {
		return false
	}
	var plan models.TrafficRolloutPlan
	if json.Unmarshal(record.PlanJSON, &plan) != nil || plan.ReleaseSHA256 != record.ReleaseSHA256 ||
		len(plan.Targets) == 0 || len(plan.Stages) == 0 || len(plan.Stages) != len(record.Stages) {
		return false
	}
	var planFields struct {
		Stages []map[string]json.RawMessage `json:"stages"`
	}
	if json.Unmarshal(record.PlanJSON, &planFields) != nil || len(planFields.Stages) != len(plan.Stages) {
		return false
	}
	for index, fields := range planFields.Stages {
		if encoded, exists := fields["requireManualApproval"]; exists {
			var required bool
			if json.Unmarshal(encoded, &required) != nil || !required {
				return false
			}
		}
		// Omission is the contract default. Persisted stage rows always record
		// the effective policy, while PlanJSON retains the submitted bytes.
		plan.Stages[index].RequireManualApproval = true
	}
	previousWeight := 0
	for index, stage := range record.Stages {
		if stage.Index != index || stage.Stage.ID == "" || !stage.Stage.RequireManualApproval ||
			stage.Stage.CandidateWeightPercent <= previousWeight || stage.Stage.CandidateWeightPercent > 100 || stage.Stage.MinimumObservationSeconds < 0 {
			return false
		}
		previousWeight = stage.Stage.CandidateWeightPercent
		if plan.Stages[index] != stage.Stage {
			return false
		}
		if index == 0 && stage.State != "active" || index > 0 && stage.State != "pending" {
			return false
		}
	}
	if previousWeight != 100 {
		return false
	}
	candidateTargets := make(map[string]bool, len(plan.Targets))
	for _, target := range plan.Targets {
		key := target.ReplicaID + "\x00" + target.Incarnation
		if target.ReplicaID == "" || target.Incarnation == "" || candidateTargets[key] {
			return false
		}
		candidateTargets[key] = true
	}
	seen := make(map[string]bool, len(record.Targets))
	storedCandidates := make(map[string]bool, len(plan.Targets))
	incumbentDigest := ""
	hasIncumbent := false
	for _, target := range record.Targets {
		key := target.Cohort + "\x00" + target.ReplicaID + "\x00" + target.IncarnationID
		if (target.Cohort != "candidate" && target.Cohort != "incumbent") || target.ReplicaID == "" || target.IncarnationID == "" ||
			!validSHA256(target.ReleaseSHA256) || target.LeaseExpiresAt.IsZero() || seen[key] {
			return false
		}
		seen[key] = true
		if target.Cohort == "candidate" && target.ReleaseSHA256 != record.ReleaseSHA256 {
			return false
		}
		if target.Cohort == "candidate" {
			storedCandidates[target.ReplicaID+"\x00"+target.IncarnationID] = true
		} else {
			hasIncumbent = true
			if incumbentDigest != "" && incumbentDigest != target.ReleaseSHA256 {
				return false
			}
			incumbentDigest = target.ReleaseSHA256
		}
	}
	if len(storedCandidates) != len(candidateTargets) || (plan.Stages[0].CandidateWeightPercent < 100 && !hasIncumbent) {
		return false
	}
	for target := range candidateTargets {
		if !storedCandidates[target] {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

// ControllerVisibleRollouts hides candidate details until the exact plugin
// configuration cohort has acknowledged its generation and the barrier closes.
func (store *SQLiteTrafficRolloutStore) ControllerVisibleRollouts(ctx context.Context) ([]models.TrafficRolloutRecord, error) {
	if store == nil || store.database == nil {
		return nil, models.TrafficRolloutInvalid{}
	}
	rows, err := store.database.QueryContext(ctx, store.queries["traffic-rollout-controller-visible"])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id == "" {
			return nil, models.TrafficRolloutInvalid{}
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	records := make([]models.TrafficRolloutRecord, 0, len(ids))
	for _, id := range ids {
		record, err := store.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

type trafficRolloutCurrentState struct {
	operationID      string
	instanceID       string
	state            string
	activeStageIndex int
	revision         int64
}

type trafficRolloutCurrentStage struct {
	id                        string
	weight                    int
	minimumObservationSeconds int
	requiresApproval          int
	state                     string
	confirmedAt               time.Time
	appliedWeight             int
}

func trafficRolloutState(ctx context.Context, tx *sql.Tx, store *SQLiteTrafficRolloutStore, id string) (trafficRolloutCurrentState, error) {
	var state trafficRolloutCurrentState
	err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-current-state"], id).Scan(
		&state.operationID, &state.instanceID, &state.state, &state.activeStageIndex, &state.revision)
	if errors.Is(err, sql.ErrNoRows) {
		return trafficRolloutCurrentState{}, models.TrafficRolloutNotFound{}
	}
	return state, err
}

func trafficRolloutStage(ctx context.Context, tx *sql.Tx, store *SQLiteTrafficRolloutStore, operationID string, index int) (trafficRolloutCurrentStage, error) {
	var stage trafficRolloutCurrentStage
	var confirmedAt string
	err := tx.QueryRowContext(ctx, store.queries["traffic-rollout-current-stage"], operationID, index).Scan(
		&stage.id, &stage.weight, &stage.minimumObservationSeconds, &stage.requiresApproval, &stage.state, &confirmedAt, &stage.appliedWeight)
	if errors.Is(err, sql.ErrNoRows) {
		return trafficRolloutCurrentStage{}, models.TrafficRolloutConflict{}
	}
	if err != nil {
		return trafficRolloutCurrentStage{}, err
	}
	if confirmedAt != "" {
		stage.confirmedAt, err = time.Parse(store.timestampLayout, confirmedAt)
	}
	return stage, err
}

func updateTrafficRolloutRevision(ctx context.Context, tx *sql.Tx, store *SQLiteTrafficRolloutStore, id string, state trafficRolloutCurrentState, nextState string, nextStage int, controllerRevision string, at time.Time, completedAt time.Time) error {
	var completed any
	if !completedAt.IsZero() {
		completed = completedAt.UTC().Format(store.timestampLayout)
	}
	result, err := tx.ExecContext(ctx, store.queries["traffic-rollout-update-state"], nextState, nextStage,
		controllerRevision, at.UTC().Format(store.timestampLayout), completed, id, state.revision, state.activeStageIndex)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return models.TrafficRolloutConflict{}
	}
	return nil
}

func validTrafficRolloutAudit(record models.AuditRecord) bool {
	return validConfigurationAudit(record)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value time.Time, layout string) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(layout)
}

func parseOptionalTime(layout, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(layout, value)
}

func nullableAppliedWeight(value int) any {
	if value < 0 {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
