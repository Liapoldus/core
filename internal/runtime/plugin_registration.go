package runtime

import (
	"context"
	"database/sql"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

// recordReplicaObservation adapts the durable observation store to the applier.
// The failure code it stores is a public error code from the management
// contract, never a driver error, a path or anything from a replica's response
// body, so the observation table can be shown to an operator as-is.
type recordReplicaObservation struct {
	store       *storage.PluginReplicaStore
	convergence *storage.PluginConvergenceSnapshot
	unavail     string
	refused     string
}

var _ plugins.ReplicaObservationRecorder = recordReplicaObservation{}

func (recorder recordReplicaObservation) RecordReplicaObservation(ctx context.Context, instanceID, replicaID string, generation int64, acknowledged, unreachable bool) {
	if recorder.store == nil {
		return
	}
	observation := storage.PluginReplicaObservation{
		InstanceID:    instanceID,
		ReplicaID:     replicaID,
		ObservedState: storage.ReplicaObservedPending,
	}
	switch {
	case acknowledged:
		observation.ObservedState = storage.ReplicaObservedAcknowledged
		observed := generation
		observation.ObservedGeneration = sqlNullInt64(observed)
	case unreachable:
		observation.ObservedState = storage.ReplicaObservedUnreachable
		observation.LastFailureCode = recorder.unavail
	default:
		observation.ObservedState = storage.ReplicaObservedFailed
		observation.LastFailureCode = recorder.refused
	}
	// An observation that cannot be written is not silently ignored: readiness
	// is derived from this table, so losing one must not be papered over here.
	if err := recorder.store.Record(context.WithoutCancel(ctx), observation); err != nil {
		recorder.convergence.Invalidate()
		return
	}
	if recorder.convergence != nil {
		_ = recorder.convergence.Refresh(context.WithoutCancel(ctx))
	}
}

func sqlNullInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}

// pluginReadiness derives status from active registration leases and durable
// generation observations. A persisted desired instance with no live replica is
// not ready; static declarations are not runtime membership.
func pluginReadiness(directory *plugins.PluginReplicaDirectory, snapshot *storage.PluginConvergenceSnapshot, words config.ManagementWords) func(context.Context) (string, string) {
	return func(_ context.Context) (string, string) {
		convergence, err := snapshot.Current()
		if err != nil {
			return words.Statuses.NotReady, "plugin observations unavailable"
		}
		live := directory.Snapshot()
		for instanceID := range convergence.Desired {
			converged, _ := registeredInstanceConverged(instanceID, convergence, live)
			if !converged {
				return words.Statuses.NotReady, "plugin instance " + instanceID + " has no live lease or current-generation acknowledgement"
			}
		}
		return words.Statuses.Ready, ""
	}
}

// pluginDrift reports whether any instance with a desired generation is not
// fully acknowledged by its live replicas.
func pluginDrift(directory *plugins.PluginReplicaDirectory, snapshot *storage.PluginConvergenceSnapshot) func(context.Context) bool {
	return func(_ context.Context) bool {
		convergence, err := snapshot.Current()
		if err != nil {
			// An unreadable observation store is drift, not a silent clean bill
			// of health: Core must not report no drift when it cannot tell.
			return true
		}
		live := directory.Snapshot()
		for instanceID := range convergence.Desired {
			_, drifted := registeredInstanceConverged(instanceID, convergence, live)
			if drifted {
				return true
			}
		}
		return false
	}
}

func registeredInstanceConverged(instanceID string, convergence storage.PluginConvergenceView, live []plugins.LivePluginReplica) (ready bool, drifted bool) {
	desired, hasDesired := convergence.Desired[instanceID]
	if !hasDesired {
		return true, false
	}
	count := 0
	for _, replica := range live {
		identity := replica.Registration.Identity
		if identity.InstanceID != instanceID {
			continue
		}
		count++
		acknowledged := false
		for _, observation := range convergence.Records {
			if observation.InstanceID == instanceID && observation.ReplicaID == identity.ReplicaID &&
				observation.ObservedState == storage.ReplicaObservedAcknowledged &&
				observation.ObservedGeneration.Valid && observation.ObservedGeneration.Int64 == desired {
				acknowledged = true
				break
			}
		}
		if !acknowledged {
			return false, true
		}
	}
	return count > 0, count == 0
}
