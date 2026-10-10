package plugins

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
)

// ReconcileRegisteredReplicas compares live, authenticated registrations with
// the durable desired generation and re-announces only replicas that have not
// proved they applied it. Reload is generation-idempotent: this retries the
// notification, never the plugin's product operation or configuration write.
func (applier *SDKConfigurationApplier) ReconcileRegisteredReplicas(ctx context.Context) error {
	if applier == nil || applier.Registered == nil || applier.Registered.Source == nil || applier.Registered.Build == nil || applier.Store == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	replicas := applier.Registered.Source.Snapshot()
	sort.Slice(replicas, func(i, j int) bool {
		left := replicas[i].Registration.Identity
		right := replicas[j].Registration.Identity
		if left.InstanceID != right.InstanceID {
			return left.InstanceID < right.InstanceID
		}
		return left.ReplicaID < right.ReplicaID
	})
	now := applier.Registered.Now().UTC()
	var failures []error
	for _, replica := range replicas {
		if err := ctx.Err(); err != nil {
			return errors.Join(errors.Join(failures...), err)
		}
		identity := replica.Registration.Identity
		if identity.InstanceID == "" || identity.ReplicaID == "" || !replica.LeaseExpires.After(now) {
			continue
		}
		if applier.SkipRegisteredInstance != nil {
			skip, err := applier.SkipRegisteredInstance(ctx, identity.InstanceID)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if skip {
				continue
			}
		}
		initial, _, initialErr := applier.Store.Current(ctx, identity.InstanceID)
		if initialErr != nil {
			if !errors.Is(initialErr, models.PluginConfigurationNotFound{}) {
				failures = append(failures, fmt.Errorf("instance %s: %w", identity.InstanceID, initialErr))
			}
			continue
		}
		if initial.Revision < 1 {
			continue
		}
		client, release, err := applier.Registered.Build(ctx, replica)
		if err == nil && client == nil {
			err = ErrPluginUnavailable
		}
		if err != nil {
			applier.recordOutcome(ctx, identity.InstanceID, identity.ReplicaID, initial.Revision, false, isUnreachable(err))
			failures = append(failures, fmt.Errorf("replica %s/%s: %w", identity.InstanceID, identity.ReplicaID, err))
			if release != nil {
				release()
			}
			continue
		}
		applier.applyMu.Lock()
		revision, _, currentErr := applier.Store.Current(ctx, identity.InstanceID)
		if currentErr != nil && !errors.Is(currentErr, models.PluginConfigurationNotFound{}) {
			applier.applyMu.Unlock()
			if release != nil {
				release()
			}
			failures = append(failures, fmt.Errorf("instance %s: %w", identity.InstanceID, currentErr))
			continue
		}
		if currentErr != nil || revision.Revision < 1 {
			applier.applyMu.Unlock()
			if release != nil {
				release()
			}
			continue
		}
		reload := sdkmodels.Reload{
			Generation: strconv.FormatInt(revision.Revision, 10), SHA256: revision.Digest,
			SchemaVersion: strconv.FormatInt(revision.SchemaVersion, 10),
		}
		acknowledged := false
		var replicaErr error
		if readinessClient, ok := client.(SDKReplicaReadinessClient); ok {
			readiness, readinessErr := readinessClient.Readiness(ctx)
			if readinessErr == nil {
				if readiness.InstanceID != identity.InstanceID || readiness.ReplicaID != identity.ReplicaID {
					replicaErr = ErrProtocolViolation
				} else {
					acknowledged = readinessMatches(readiness, identity.InstanceID, identity.ReplicaID, revision)
				}
			}
		}
		if !acknowledged && replicaErr == nil {
			answer, reloadErr := client.Reload(ctx, reload)
			if reloadErr == nil && !reloadAcknowledgementMatches(reload, answer) {
				reloadErr = ErrProtocolViolation
			}
			replicaErr = reloadErr
			acknowledged = reloadErr == nil
		}
		applier.applyMu.Unlock()
		if release != nil {
			release()
		}
		applier.recordOutcome(ctx, identity.InstanceID, identity.ReplicaID, revision.Revision, acknowledged, isUnreachable(replicaErr))
		if replicaErr != nil {
			failures = append(failures, fmt.Errorf("replica %s/%s: %w", identity.InstanceID, identity.ReplicaID, replicaErr))
		}
	}
	return errors.Join(failures...)
}

// ReloadActiveReplica announces the current desired generation only to the
// exact incarnation that has just registered. Peers already in the directory
// are reconciled independently, avoiding an instance-wide reload for each
// scale-out registration.
func (applier *SDKConfigurationApplier) ReloadActiveReplica(ctx context.Context, instanceID, replicaID string) error {
	if applier == nil || applier.Store == nil || applier.Registered == nil || instanceID == "" || replicaID == "" {
		return ErrPluginUnavailable
	}
	if applier.SkipRegisteredInstance != nil {
		skip, err := applier.SkipRegisteredInstance(ctx, instanceID)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	applier.applyMu.Lock()
	defer applier.applyMu.Unlock()

	client, release, found, err := applier.Registered.ResolveReplica(ctx, instanceID, replicaID)
	if err != nil {
		return err
	}
	if !found || client == nil {
		return ErrPluginUnavailable
	}
	if release != nil {
		defer release()
	}
	revision, _, err := applier.Store.Current(ctx, instanceID)
	if err != nil {
		if errors.Is(err, models.PluginConfigurationNotFound{}) {
			return nil
		}
		return err
	}
	if revision.Revision < 1 {
		return nil
	}
	reload := sdkmodels.Reload{
		Generation: strconv.FormatInt(revision.Revision, 10), SHA256: revision.Digest,
		SchemaVersion: strconv.FormatInt(revision.SchemaVersion, 10),
	}
	if readinessClient, ok := client.(SDKReplicaReadinessClient); ok {
		readiness, readinessErr := readinessClient.Readiness(ctx)
		if readinessErr == nil {
			if readiness.InstanceID != instanceID || readiness.ReplicaID != replicaID {
				return ErrProtocolViolation
			}
			if readinessMatches(readiness, instanceID, replicaID, revision) {
				applier.recordOutcome(ctx, instanceID, replicaID, revision.Revision, true, false)
				return nil
			}
		}
	}
	acknowledgement, err := client.Reload(ctx, reload)
	if err == nil && !reloadAcknowledgementMatches(reload, acknowledgement) {
		err = ErrProtocolViolation
	}
	applier.recordOutcome(ctx, instanceID, replicaID, revision.Revision, err == nil, isUnreachable(err))
	return err
}

// RunRegisteredReconciliation periodically advances live registered replicas
// toward the current active generation. The interval is supplied by the
// versioned Core reconciliation policy; cancellation stops the worker cleanly.
func (applier *SDKConfigurationApplier) RunRegisteredReconciliation(ctx context.Context, interval time.Duration, recover ...func(context.Context) error) error {
	if interval <= 0 || len(recover) > 1 {
		return ErrProtocolViolation
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = applier.ReconcileRegisteredReplicas(ctx)
			if len(recover) == 1 && recover[0] != nil {
				_ = recover[0](ctx)
			}
		}
	}
}
