package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PluginReplicaObservation is what Core last saw a declared replica doing. It is
// an observation only: the replica set, its endpoint and its expected identity
// are owned by core.yaml, so an observation never redefines desired topology.
type PluginReplicaObservation struct {
	InstanceID         string
	ReplicaID          string
	ObservedGeneration sql.NullInt64
	ObservedState      string
	LastFailureCode    string
	ObservedAt         time.Time
}

type PluginReplicaStore struct {
	database      *sql.DB
	insertReplica string
	selectReplica string
	invalidRecord string
}

// Replica observation states. A replica is eligible to serve only in
// acknowledged state at the desired generation; pending, failed and unreachable
// are all fenced observations.
const (
	ReplicaObservedPending      = "pending"
	ReplicaObservedAcknowledged = "acknowledged"
	ReplicaObservedFailed       = "failed"
	ReplicaObservedUnreachable  = "unreachable"
)

const insertPluginReplicaSQL = `
INSERT INTO plugin_replicas (instance_id, replica_id, observed_generation, observed_state, last_failure_code, observed_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (instance_id, replica_id) DO UPDATE SET
    observed_generation = excluded.observed_generation,
    observed_state = excluded.observed_state,
    last_failure_code = excluded.last_failure_code,
    observed_at = excluded.observed_at`

const selectPluginReplicasSQL = `
SELECT instance_id, replica_id, observed_generation, observed_state, last_failure_code, observed_at
FROM plugin_replicas
ORDER BY instance_id, replica_id`

// NewPluginReplicaStore builds the observation store over an open database.
func NewPluginReplicaStore(database *sql.DB, invalidRecord string) *PluginReplicaStore {
	return &PluginReplicaStore{
		database:      database,
		insertReplica: insertPluginReplicaSQL,
		selectReplica: selectPluginReplicasSQL,
		invalidRecord: invalidRecord,
	}
}

// Record writes the latest observation of one replica. Recording is idempotent
// per (instance, replica) so Core can refresh it freely without implying that
// the replica was told anything.
func (store *PluginReplicaStore) Record(ctx context.Context, observation PluginReplicaObservation) error {
	if store == nil || store.database == nil {
		return errors.New(store.invalidRecord)
	}
	if observation.InstanceID == "" || observation.ReplicaID == "" || !validReplicaState(observation.ObservedState) {
		return errors.New(store.invalidRecord)
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now().UTC()
	}
	_, err := store.database.ExecContext(ctx, store.insertReplica,
		observation.InstanceID, observation.ReplicaID, observation.ObservedGeneration,
		observation.ObservedState, observation.LastFailureCode,
		observation.ObservedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

// List returns every recorded replica observation in stable order.
func (store *PluginReplicaStore) List(ctx context.Context) ([]PluginReplicaObservation, error) {
	if store == nil || store.database == nil {
		return nil, errors.New(store.invalidRecord)
	}
	rows, err := store.database.QueryContext(ctx, store.selectReplica)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	observations := make([]PluginReplicaObservation, 0)
	for rows.Next() {
		var observation PluginReplicaObservation
		var observedAt string
		if err := rows.Scan(&observation.InstanceID, &observation.ReplicaID, &observation.ObservedGeneration,
			&observation.ObservedState, &observation.LastFailureCode, &observedAt); err != nil {
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339Nano, observedAt)
		if err != nil {
			return nil, errors.New(store.invalidRecord)
		}
		observation.ObservedAt = parsed
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return observations, nil
}

func validReplicaState(state string) bool {
	switch state {
	case ReplicaObservedPending, ReplicaObservedAcknowledged, ReplicaObservedFailed, ReplicaObservedUnreachable:
		return true
	default:
		return false
	}
}
