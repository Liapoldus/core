package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// Targets returns the immutable membership committed with one promotion. The
// found bit distinguishes an empty v2 cohort from an operation created by the
// legacy static-endpoint path.
func (store *SQLitePluginConfigurationStore) Targets(ctx context.Context, operationID string) ([]models.PluginRolloutTarget, bool, error) {
	if store == nil || store.database == nil || operationID == "" {
		return nil, false, sql.ErrConnDone
	}
	var instanceID string
	var generation int64
	var open bool
	err := store.database.QueryRowContext(ctx, store.queries.queries["get-rollout"], operationID).Scan(&instanceID, &generation, &open)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil || instanceID == "" || generation < 1 {
		return nil, false, errors.New(store.contract.Diagnostics.InvalidStore)
	}
	rows, err := store.database.QueryContext(ctx, store.queries.queries["list-rollout-targets"], operationID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	targets := make([]models.PluginRolloutTarget, 0)
	for rows.Next() {
		var target models.PluginRolloutTarget
		var acknowledged int
		var leaseExpiresAt string
		if err := rows.Scan(&target.ReplicaID, &target.IncarnationID, &target.ReleaseSHA256, &acknowledged, &leaseExpiresAt); err != nil {
			return nil, false, err
		}
		target.LeaseExpiresAt, err = time.Parse(store.contract.TimestampLayout, leaseExpiresAt)
		if err != nil {
			return nil, false, errors.New(store.contract.Diagnostics.InvalidStore)
		}
		if acknowledged != 0 && acknowledged != 1 {
			return nil, false, errors.New(store.contract.Diagnostics.InvalidStore)
		}
		target.Acknowledged = acknowledged == 1
		if !target.Valid() {
			return nil, false, errors.New(store.contract.Diagnostics.InvalidStore)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	_ = open // retained in the query to validate the durable rollout record
	return targets, true, nil
}

// AcknowledgeTargets persists exact-incarnation acknowledgements. A same-ID
// replacement cannot update a row because both incarnation and release digest
// are part of the conditional update.
func (store *SQLitePluginConfigurationStore) AcknowledgeTargets(ctx context.Context, operationID string, targets []models.PluginRolloutTarget) error {
	if store == nil || store.database == nil || operationID == "" {
		return sql.ErrConnDone
	}
	if len(targets) == 0 {
		return nil
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, target := range targets {
		if !target.Valid() || !target.Acknowledged {
			return errors.New(store.contract.Diagnostics.InvalidStore)
		}
		result, err := tx.ExecContext(ctx, store.queries.queries["acknowledge-rollout-target"], operationID,
			target.ReplicaID, target.IncarnationID, target.ReleaseSHA256)
		if err != nil {
			return err
		}
		if err := requireOneRow(result); err != nil {
			return errors.New(store.contract.Diagnostics.InvalidStore)
		}
	}
	return tx.Commit()
}

// CompleteRollout releases the per-instance rollout barrier only after the
// application service has verified that every frozen target acknowledged.
func (store *SQLitePluginConfigurationStore) CompleteRollout(ctx context.Context, operationID string) error {
	if store == nil || store.database == nil || operationID == "" {
		return sql.ErrConnDone
	}
	result, err := store.database.ExecContext(ctx, store.queries.queries["complete-rollout"], time.Now().UTC().Format(store.contract.TimestampLayout), operationID)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		var open bool
		if readErr := store.database.QueryRowContext(ctx, store.queries.queries["get-rollout"], operationID).Scan(new(string), new(int64), &open); readErr == nil {
			if !open {
				return nil
			}
			return models.PluginConfigurationConflict{}
		}
		return errors.New(store.contract.Diagnostics.InvalidStore)
	}
	return nil
}

// CloseRollout releases the per-instance rollout barrier after a terminal
// apply refusal. The promoted active generation remains in place for explicit
// rollback; only the durable barrier is closed.
func (store *SQLitePluginConfigurationStore) CloseRollout(ctx context.Context, operationID string) error {
	if store == nil || store.database == nil || operationID == "" {
		return sql.ErrConnDone
	}
	result, err := store.database.ExecContext(ctx, store.queries.queries["close-rollout"],
		time.Now().UTC().Format(store.contract.TimestampLayout), operationID)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		var open bool
		if readErr := store.database.QueryRowContext(ctx, store.queries.queries["get-rollout"], operationID).
			Scan(new(string), new(int64), &open); readErr == nil && !open {
			return nil
		}
		return models.PluginConfigurationConflict{}
	}
	return nil
}

// FailRolloutTargetLost atomically closes a rollout and stores its terminal
// operation failure only when an exact unacknowledged frozen target is supplied.
func (store *SQLitePluginConfigurationStore) FailRolloutTargetLost(
	ctx context.Context,
	operationID string,
	lost []models.PluginRolloutTarget,
	pendingState, runningState, failedState, errorCode string,
) error {
	if store == nil || store.database == nil || operationID == "" || len(lost) == 0 ||
		pendingState == "" || runningState == "" || failedState == "" || errorCode == "" {
		return sql.ErrConnDone
	}
	operationStore, err := NewSQLiteOperationStore(store.database)
	if err != nil {
		return err
	}
	problem, err := json.Marshal(map[string]string{operationStore.contract.ErrorCodeField: errorCode})
	if err != nil {
		return err
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	var open bool
	if err := tx.QueryRowContext(ctx, store.queries.queries["select-rollout-operation-state"], operationID).Scan(&state, &open); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.PluginConfigurationNotFound{}
		}
		return err
	}
	if !open || state != pendingState && state != runningState {
		return models.PluginConfigurationConflict{}
	}
	for _, target := range lost {
		if !target.Valid() {
			return errors.New(store.contract.Diagnostics.InvalidStore)
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, store.queries.queries["is-unacknowledged-rollout-target"],
			operationID, target.ReplicaID, target.IncarnationID, target.ReleaseSHA256).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return models.PluginConfigurationConflict{}
		}
	}
	now := time.Now().UTC().Format(operationStore.contract.TimestampLayout)
	result, err := tx.ExecContext(ctx, store.queries.queries["close-rollout-target-lost"], now, operationID)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return models.PluginConfigurationConflict{}
	}
	if _, err := tx.ExecContext(ctx, store.queries.queries["fail-associated-traffic-rollout"],
		failedState, now, now, operationID, runningState); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, store.queries.queries["fail-rollout-operation"],
		failedState, now, problem, operationID, pendingState, runningState)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return models.PluginConfigurationConflict{}
	}
	return tx.Commit()
}

var _ interfaces.PluginConfigurationRolloutStore = (*SQLitePluginConfigurationStore)(nil)
