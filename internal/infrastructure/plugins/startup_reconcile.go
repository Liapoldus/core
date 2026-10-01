package plugins

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

// SDKReplicaReadinessClient is one declared replica client that can report the
// generation it actually applied. The exact-generation pull client implements
// it; a client that can only announce generations does not, and is re-announced
// unconditionally instead of being compared.
type SDKReplicaReadinessClient interface {
	Readiness(context.Context) (sdkmodels.Readiness, error)
}

// ReconcileDeclaredReplicas re-establishes the invariant that every declared
// replica of every instance with a committed active generation has applied that
// exact generation.
//
// Core calls it once on startup, after the exact-generation pull listener is
// serving and before it accepts Management traffic, so a restarted replica can
// pull the active generation again once Core and the plugin have both been
// down. The desired generation comes from the durable store, never from a
// replica: a replica can never widen the set of generations it is allowed to
// apply.
//
// It is compare-first. A replica that reports readiness for the desired
// generation, digest and identity is left alone, so an ordinary Core restart
// that changes nothing does not reload a healthy data plane. Only a replica
// that is unreachable, not ready, or at a different generation is re-announced.
//
// It is best-effort and per-replica. An unreachable or refusing replica is
// recorded through observations and reported in the returned error, but it
// never prevents Core from starting: fencing such a replica is the caller's
// decision, not a reason to abort the process.
func (applier *SDKConfigurationApplier) ReconcileDeclaredReplicas(ctx context.Context) error {
	if applier == nil || len(applier.Clients) == 0 {
		return nil
	}
	instanceIDs := make([]string, 0, len(applier.Clients))
	for instanceID := range applier.Clients {
		instanceIDs = append(instanceIDs, instanceID)
	}
	sort.Strings(instanceIDs)
	var failures []error
	for _, instanceID := range instanceIDs {
		active, err := desiredRevision(ctx, applier.Store, instanceID)
		if err != nil {
			failures = append(failures, fmt.Errorf("instance %s: %w", instanceID, err))
			continue
		}
		if active.Revision == 0 {
			continue
		}
		reload := sdkmodels.Reload{
			Generation:    strconv.FormatInt(active.Revision, 10),
			SHA256:        active.Digest,
			SchemaVersion: strconv.FormatInt(active.SchemaVersion, 10),
		}
		fanout, isFanout := applier.Clients[instanceID].(*SDKReloadFanout)
		if !isFanout {
			if _, err := applier.Clients[instanceID].Reload(ctx, reload); err != nil {
				failures = append(failures, fmt.Errorf("instance %s: %w", instanceID, err))
			}
			continue
		}
		for _, replica := range fanout.Replicas {
			if replicaAppliedDesired(ctx, replica, instanceID, active) {
				applier.recordOutcome(ctx, instanceID, replica.ReplicaID, active.Revision, true, false)
				continue
			}
			_, err := replica.Client.Reload(ctx, reload)
			applier.recordOutcome(ctx, instanceID, replica.ReplicaID, active.Revision, err == nil, isUnreachable(err))
			if err != nil {
				failures = append(failures, fmt.Errorf("replica %s/%s: %w", instanceID, replica.ReplicaID, err))
			}
		}
	}
	return errors.Join(failures...)
}

// desiredRevision loads the committed active revision for one instance. An
// instance with no active generation has nothing to converge and is skipped.
func desiredRevision(ctx context.Context, reader interfaces.PluginConfigurationReader, instanceID string) (models.PluginConfigurationRevision, error) {
	if reader == nil {
		return models.PluginConfigurationRevision{}, nil
	}
	active, _, err := reader.Current(ctx, instanceID)
	if err != nil {
		if errors.Is(err, models.PluginConfigurationNotFound{}) {
			return models.PluginConfigurationRevision{}, nil
		}
		return models.PluginConfigurationRevision{}, err
	}
	if active.Revision == 0 {
		return models.PluginConfigurationRevision{}, nil
	}
	return active, nil
}

// replicaAppliedDesired reports whether a replica's own readiness answer already
// matches the desired generation and identity. A replica that cannot report
// readiness, or whose answer cannot be trusted, is treated as not converged so
// it is re-announced rather than silently skipped.
func replicaAppliedDesired(ctx context.Context, replica SDKReloadReplicaClient, instanceID string, active models.PluginConfigurationRevision) bool {
	readinessClient, ok := replica.Client.(SDKReplicaReadinessClient)
	if !ok {
		return false
	}
	readiness, err := readinessClient.Readiness(ctx)
	if err != nil {
		return false
	}
	return readiness.Ready &&
		readiness.Generation == strconv.FormatInt(active.Revision, 10) &&
		readiness.SHA256 == active.Digest &&
		readiness.InstanceID == instanceID &&
		readiness.ReplicaID == replica.ReplicaID
}

// recordOutcome records one replica outcome when an observer is configured.
// A missing observer is a legitimate configuration (an operation without a
// durability sink), so it silences the record rather than failing the pass.
func (applier *SDKConfigurationApplier) recordOutcome(ctx context.Context, instanceID, replicaID string, generation int64, acknowledged, unreachable bool) {
	if applier.Observations == nil {
		return
	}
	applier.Observations.RecordReplicaObservation(ctx, instanceID, replicaID, generation, acknowledged, unreachable)
}
