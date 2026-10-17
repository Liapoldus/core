package runtime

import (
	"context"
	"database/sql"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/services"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
)

// recordReplicaObservation adapts the durable observation store to the applier.
// The failure code it stores is a public error code from the management
// contract, never a driver error, a path or anything from a replica's response
// body, so the observation table can be shown to an operator as-is.
type recordReplicaObservation struct {
	store       *storage.ServiceReplicaStore
	convergence *storage.ServiceConvergenceSnapshot
	unavail     string
	refused     string
}

var _ services.ReplicaObservationRecorder = recordReplicaObservation{}

func (recorder recordReplicaObservation) RecordReplicaObservation(ctx context.Context, instanceID, replicaID, incarnationID string, generation int64, acknowledged, unreachable bool) {
	if recorder.store == nil {
		return
	}
	observation := storage.ServiceReplicaObservation{
		InstanceID:    instanceID,
		ReplicaID:     replicaID,
		IncarnationID: incarnationID,
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

// serviceReadiness derives status from active registration leases and durable
// generation observations. A persisted desired instance with no live replica is
// not ready; static declarations are not runtime membership.
func serviceReadiness(directory *services.ServiceReplicaDirectory, snapshot *storage.ServiceConvergenceSnapshot, words config.ManagementWords) func(context.Context) (string, string) {
	return func(_ context.Context) (string, string) {
		convergence, err := snapshot.Current()
		if err != nil {
			return words.Statuses.NotReady, "service observations unavailable"
		}
		live := directory.Snapshot()
		for instanceID := range convergence.Desired {
			converged, _ := registeredInstanceConverged(instanceID, convergence, live)
			if !converged {
				return words.Statuses.NotReady, "service instance " + instanceID + " has no live lease or current-generation acknowledgement"
			}
		}
		return words.Statuses.Ready, ""
	}
}

// serviceDrift reports whether any instance with a desired generation is not
// fully acknowledged by its live replicas.
func serviceDrift(directory *services.ServiceReplicaDirectory, snapshot *storage.ServiceConvergenceSnapshot) func(context.Context) bool {
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

func registeredInstanceConverged(instanceID string, convergence storage.ServiceConvergenceView, live []services.LiveServiceReplica) (ready bool, drifted bool) {
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
			if observation.InstanceID == instanceID && observation.ReplicaID == identity.ReplicaID && observation.IncarnationID == identity.IncarnationID &&
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

func serviceReplicaConfigurationStatus(convergence storage.ServiceConvergenceView, instanceID, replicaID, incarnationID string) models.ServiceReplicaConfigurationStatus {
	status := models.ServiceReplicaConfigurationStatus{State: models.ServiceConfigurationUnconfigured}
	desired, configured := convergence.Desired[instanceID]
	if !configured || desired < 1 {
		return status
	}
	status.DesiredGeneration = int64Pointer(desired)
	status.State = models.ServiceConfigurationUnobserved
	for _, observation := range convergence.Records {
		if observation.InstanceID != instanceID || observation.ReplicaID != replicaID || observation.IncarnationID != incarnationID {
			continue
		}
		if !observation.ObservedAt.IsZero() {
			observedAt := observation.ObservedAt
			status.ObservedAt = &observedAt
		}
		status.LastFailureCode = observation.LastFailureCode
		if observation.ObservedGeneration.Valid {
			status.AppliedGeneration = int64Pointer(observation.ObservedGeneration.Int64)
		}
		switch observation.ObservedState {
		case storage.ReplicaObservedAcknowledged:
			status.State = models.ServiceConfigurationStale
			if observation.ObservedGeneration.Valid && observation.ObservedGeneration.Int64 == desired {
				status.State = models.ServiceConfigurationAcknowledged
			}
		case storage.ReplicaObservedPending:
			status.State = models.ServiceConfigurationPending
		case storage.ReplicaObservedFailed:
			status.State = models.ServiceConfigurationFailed
		case storage.ReplicaObservedUnreachable:
			status.State = models.ServiceConfigurationUnreachable
		}
		return status
	}
	return status
}

func int64Pointer(value int64) *int64 { return &value }
