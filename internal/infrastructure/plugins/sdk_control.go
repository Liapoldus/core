package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	sdkmodels "liapoldus.local/plugin-sdk/domain/models"
)

type SDKReloadClient interface {
	Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error)
}

// SDKReloadFanout announces one generation to every declared replica of a single
// plugin instance. A generation is applied to the instance only when every
// declared replica accepted it: a partial acknowledgement leaves the instance
// fenced, and the failing replica is reported by name so the operator can see
// which declared endpoint did not converge. Replicas are notified sequentially in
// declaration order so a single failure has a deterministic cause.
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

// Reload announces the generation to each declared replica and stops at the
// first refusal. The returned error names the replica that refused so a
// degraded instance is attributable to a declared endpoint. The call is never
// replayed here: whether to repeat a failed announcement is a Core operation
// decision, not a client one.
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

// ReloadObserved announces the generation and reports every replica outcome,
// including the replicas that were never reached after the first refusal. Only
// the refusal itself is returned as an error; the remaining replicas are
// deliberately not contacted so an unavailable replica never turns a single
// announcement into an unbounded fan-out.
func (fanout *SDKReloadFanout) ReloadObserved(ctx context.Context, reload sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, []ReplicaReloadResult, error) {
	results := make([]ReplicaReloadResult, 0, len(fanout.Replicas))
	if fanout == nil || len(fanout.Replicas) == 0 {
		return sdkmodels.ReloadAcknowledgement{}, results, ErrPluginUnavailable
	}
	var acknowledged sdkmodels.ReloadAcknowledgement
	for index, replica := range fanout.Replicas {
		if replica.Client == nil {
			results = append(results, ReplicaReloadResult{ReplicaID: replica.ReplicaID, Unreachable: true})
			markRemainingUnreachable(fanout.Replicas, results, index+1)
			return sdkmodels.ReloadAcknowledgement{}, results, ErrPluginUnavailable
		}
		answer, err := replica.Client.Reload(ctx, reload)
		if err != nil {
			results = append(results, ReplicaReloadResult{ReplicaID: replica.ReplicaID, Unreachable: isUnreachable(err)})
			markRemainingUnreachable(fanout.Replicas, results, index+1)
			return sdkmodels.ReloadAcknowledgement{}, results, fmt.Errorf("replica %s/%s: %w", fanout.InstanceID, replica.ReplicaID, err)
		}
		results = append(results, ReplicaReloadResult{ReplicaID: replica.ReplicaID, Acknowledged: true})
		acknowledged = answer
	}
	return acknowledged, results, nil
}

// markRemainingUnreachable records the replicas that were never attempted, so a
// partial announcement never looks like a partial acknowledgement.
func markRemainingUnreachable(replicas []SDKReloadReplicaClient, results []ReplicaReloadResult, from int) {
	for _, remaining := range replicas[from:] {
		results = append(results, ReplicaReloadResult{ReplicaID: remaining.ReplicaID, Unreachable: true})
	}
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

type SDKConfigurationApplier struct {
	Store        interfaces.PluginConfigurationStore
	Clients      map[string]SDKReloadClient
	Observations ReplicaObservationRecorder
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
