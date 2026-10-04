package bootstrap

import (
	"context"
	"database/sql"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

// registerDeclaredPlugins records the operator-declared registry into SQLite.
//
// It is INSERT OR IGNORE on purpose. The declared set is owned by core.yaml, so
// Core registers rows for what the operator declared and never invents, drops or
// rewrites an instance. It also never overwrites an existing observation: a
// replica that already acknowledged a generation before a Core restart is still
// observed as having acknowledged it, which is what keeps startup from looking
// like an unapplied configuration and re-announcing generations that no operator
// asked to re-announce.
// registerDeclaredPlugins is called once during startup, after the database and
// configuration stores are open. A declared instance with no configuration yet
// is registered in the configured state; the manifest stays an empty JSON object
// because Core never fabricates a plugin-owned manifest, and an unreachable
// replica is a degraded observation rather than a startup failure.
func registerDeclaredPlugins(ctx context.Context, database *sql.DB, registry pluginRegistry) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, instance := range registry.instances {
		if err := storage.RegisterPluginInstance(ctx, database, instance.InstanceID, []byte("{}"), "configured", now); err != nil {
			return err
		}
		for _, replica := range instance.Replicas {
			if err := storage.RegisterPluginReplica(ctx, database, instance.InstanceID, replica.ReplicaID, storage.ReplicaObservedPending, now); err != nil {
				return err
			}
		}
	}
	return nil
}

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

// pluginConvergence is the derived view Core reports on /api/status. It is
// computed from durable desired state (the active generation) and the recorded
// replica observations, never from a constant.
// instanceConverged reports whether every declared replica of an instance is
// observed at exactly the desired generation. An instance that has no desired
// configuration yet has nothing to converge and does not hold readiness. An
// instance that has one but whose replicas have not all acknowledged it is
// fenced, and it is exactly that case that makes Core degraded with drift.
func instanceConverged(instanceID string, convergence storage.PluginConvergenceView, declaredReplicas int) (ready bool, drifted bool) {
	desired, hasDesired := convergence.Desired[instanceID]
	if !hasDesired {
		return true, false
	}
	acknowledged := 0
	for _, record := range convergence.Records {
		if record.InstanceID != instanceID {
			continue
		}
		if record.ObservedState == storage.ReplicaObservedAcknowledged && record.ObservedGeneration.Valid && record.ObservedGeneration.Int64 == desired {
			acknowledged++
		}
	}
	if acknowledged == declaredReplicas {
		return true, false
	}
	return false, true
}

// pluginReadiness derives the management readiness state and reason from the
// declared registry plus durable observations. A Core with declared replicas that
// have not converged reports notReady, and drift is true whenever any instance
// with a desired generation is not fully acknowledged.
func pluginReadiness(registry pluginRegistry, snapshot *storage.PluginConvergenceSnapshot, words config.ManagementWords) func(context.Context) (string, string) {
	return func(_ context.Context) (string, string) {
		convergence, err := snapshot.Current()
		if err != nil {
			return words.Statuses.NotReady, "plugin observations unavailable"
		}
		for _, instance := range registry.instances {
			converged, _ := instanceConverged(instance.InstanceID, convergence, len(instance.Replicas))
			if !converged {
				return words.Statuses.NotReady, "plugin replica has not acknowledged the desired generation"
			}
		}
		return words.Statuses.Ready, ""
	}
}

// pluginDrift reports whether any instance with a desired generation is not
// fully acknowledged by its declared replicas.
func pluginDrift(registry pluginRegistry, snapshot *storage.PluginConvergenceSnapshot) func(context.Context) bool {
	return func(_ context.Context) bool {
		convergence, err := snapshot.Current()
		if err != nil {
			// An unreadable observation store is drift, not a silent clean bill
			// of health: Core must not report no drift when it cannot tell.
			return true
		}
		for _, instance := range registry.instances {
			_, drifted := instanceConverged(instance.InstanceID, convergence, len(instance.Replicas))
			if drifted {
				return true
			}
		}
		return false
	}
}
