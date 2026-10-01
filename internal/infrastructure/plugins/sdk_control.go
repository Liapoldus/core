package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
)

type SDKReloadClient interface {
	Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error)
}

// SDKReloadFanout announces one generation to every declared replica of a single
// plugin instance. A generation is converged only when every declared replica
// accepted it. Replicas are notified in declaration order; one refusal does not
// prevent later replicas from receiving the desired generation.
type SDKReloadFanout struct {
	InstanceID string
	Replicas   []SDKReloadReplicaClient
}

// SDKReloadReplicaClient is one declared replica's control client plus the
// replica id the operator declared for it in core.yaml.
type SDKReloadReplicaClient struct {
	ReplicaID string
	Client    SDKReloadClient
}

// Reload announces the generation to every declared replica. Its returned
// error identifies all failed replicas so degradation remains attributable to
// declared endpoints. Calls are never replayed here: whether to retry an
// announcement is a Core operation decision, not a client one.
func (fanout *SDKReloadFanout) Reload(ctx context.Context, reload sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	acknowledged, _, err := fanout.ReloadObserved(ctx, reload)
	return acknowledged, err
}

// ReplicaReloadResult is the per-replica outcome of one announcement. Core
// records these as observations so readiness and drift can be derived from what
// each declared replica actually did.
type ReplicaReloadResult struct {
	ReplicaID    string
	Acknowledged bool
	// Unreachable is set when the replica could not be contacted at all, which is
	// a different operator-visible condition from a replica that was reached and
	// refused the generation.
	Unreachable bool
}

// ReloadObserved announces the generation to every declared replica and reports
// each outcome. Failed replicas remain fenced while successfully acknowledged
// replicas may serve the desired generation.
func (fanout *SDKReloadFanout) ReloadObserved(ctx context.Context, reload sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, []ReplicaReloadResult, error) {
	if fanout == nil || len(fanout.Replicas) == 0 {
		return sdkmodels.ReloadAcknowledgement{}, nil, ErrPluginUnavailable
	}
	results := make([]ReplicaReloadResult, 0, len(fanout.Replicas))
	var acknowledged sdkmodels.ReloadAcknowledgement
	var failures []error
	for _, replica := range fanout.Replicas {
		if replica.Client == nil {
			results = append(results, ReplicaReloadResult{ReplicaID: replica.ReplicaID, Unreachable: true})
			failures = append(failures, fmt.Errorf("replica %s/%s: %w", fanout.InstanceID, replica.ReplicaID, ErrPluginUnavailable))
			continue
		}
		answer, err := replica.Client.Reload(ctx, reload)
		if err != nil {
			results = append(results, ReplicaReloadResult{ReplicaID: replica.ReplicaID, Unreachable: isUnreachable(err)})
			failures = append(failures, fmt.Errorf("replica %s/%s: %w", fanout.InstanceID, replica.ReplicaID, err))
			continue
		}
		results = append(results, ReplicaReloadResult{ReplicaID: replica.ReplicaID, Acknowledged: true})
		acknowledged = answer
	}
	if len(failures) != 0 {
		return sdkmodels.ReloadAcknowledgement{}, results, errors.Join(failures...)
	}
	return acknowledged, results, nil
}

// isUnreachable reports whether an SDK client error means the replica could not
// be contacted. It is a narrow check on the sentinel the transport layer
// produces, so a reached-but-refusing replica is not misreported as down.
func isUnreachable(err error) bool {
	return errors.Is(err, ErrPluginUnavailable)
}

// ReplicaObservationRecorder records what a declared replica was last observed
// doing. Observations never redefine the declared replica set, endpoint or
// identity; they only make the declared set observable.
type ReplicaObservationRecorder interface {
	RecordReplicaObservation(ctx context.Context, instanceID, replicaID string, generation int64, acknowledged, unreachable bool)
}

// ConfigurationSnapshot is refreshed after a durable promotion and before any
// replica is told to pull that generation.
type ConfigurationSnapshot interface {
	Refresh(context.Context, string) error
}

type ConvergenceSnapshot interface {
	Refresh(context.Context) error
	Invalidate()
}

type SDKConfigurationApplier struct {
	Store        interfaces.PluginConfigurationStore
	Clients      map[string]SDKReloadClient
	Observations ReplicaObservationRecorder
	Snapshot     ConfigurationSnapshot
	Convergence  ConvergenceSnapshot
}

var _ interfaces.PluginConfigurationApplier = (*SDKConfigurationApplier)(nil)

func (applier *SDKConfigurationApplier) ApplyConfiguration(ctx context.Context, instanceID, generation string, rawJSON []byte) error {
	if applier == nil || applier.Store == nil || instanceID == "" {
		return ErrPluginUnavailable
	}
	client := applier.Clients[instanceID]
	if client == nil {
		return ErrPluginUnavailable
	}
	generationNumber, err := strconv.ParseInt(generation, 10, 64)
	if err != nil || generationNumber < 1 || strconv.FormatInt(generationNumber, 10) != generation {
		return ErrProtocolViolation
	}
	revision, err := applier.Store.GetRevision(ctx, instanceID, generationNumber)
	if err != nil {
		return err
	}
	if !bytes.Equal(revision.SettingsJSON, rawJSON) {
		return ErrProtocolViolation
	}
	if applier.Convergence != nil {
		applier.Convergence.Invalidate()
	}
	if applier.Snapshot != nil {
		if err := applier.Snapshot.Refresh(context.WithoutCancel(ctx), instanceID); err != nil {
			return err
		}
	}
	if applier.Convergence != nil {
		if err := applier.Convergence.Refresh(context.WithoutCancel(ctx)); err != nil {
			return err
		}
	}
	reload := sdkmodels.Reload{
		Generation:    generation,
		SHA256:        revision.Digest,
		SchemaVersion: strconv.FormatInt(revision.SchemaVersion, 10),
	}
	// When the instance fans out to declared replicas, record every replica
	// outcome so readiness reflects which declared endpoints actually converged
	// rather than whether one representative replica answered.
	if fanout, isFanout := client.(*SDKReloadFanout); isFanout && applier.Observations != nil {
		_, results, fanoutErr := fanout.ReloadObserved(ctx, reload)
		for _, result := range results {
			applier.Observations.RecordReplicaObservation(ctx, instanceID, result.ReplicaID, generationNumber, result.Acknowledged, result.Unreachable)
		}
		return fanoutErr
	}
	_, err = client.Reload(ctx, reload)
	return err
}
