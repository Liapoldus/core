package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
)

type SDKReloadClient interface {
	Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error)
}

// SDKReloadFanout announces one generation concurrently to every declared
// replica of a single plugin instance. A generation is converged only when every
// declared replica accepted it. Results and errors retain declaration order.
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

// RegisteredReplicaSource exposes the current authenticated lease snapshot and
// remembers whether an instance has ever registered during this Core process.
// The history bit prevents an expired or not-ready v2 instance from silently
// falling back to a static endpoint list.
type RegisteredReplicaSource interface {
	Snapshot() []LivePluginReplica
	HasRegisteredInstance(string) bool
}

type RegisteredReplicaClientFactory func(context.Context, LivePluginReplica) (SDKReloadClient, func(), error)

// RegisteredReplicaReloadResolver builds a fresh control fanout from one live
// directory snapshot for each configuration activation. It does not persist
// endpoints or replay work; the directory is the authority for eligible leases.
type RegisteredReplicaReloadResolver struct {
	Source RegisteredReplicaSource
	Build  RegisteredReplicaClientFactory
	Now    func() time.Time
	// ValidateSchema is supplied by the composition root; no configuration
	// loader is reachable from the plugin transport adapter.
	ValidateSchema func(document, schema []byte) error
}

type pluginConfigurationSchemaClient interface {
	ConfigurationSchema(context.Context) ([]byte, error)
}

// TrafficRolloutReplicas adapts authenticated Plugin SDK registrations into the
// neutral cohort model. Compatibility remains defined by the SDK's generic
// release contract, not by any product plugin.
func (resolver *RegisteredReplicaReloadResolver) TrafficRolloutReplicas(ctx context.Context, instanceID string) ([]models.TrafficRolloutReplica, error) {
	if resolver == nil || resolver.Source == nil || instanceID == "" {
		return nil, ErrPluginUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := resolver.Now().UTC()
	registrations := make([]sdkmodels.ReplicaRegistrationRequest, 0)
	replicas := make([]models.TrafficRolloutReplica, 0)
	for _, live := range resolver.Source.Snapshot() {
		identity := live.Registration.Identity
		if identity.InstanceID != instanceID || !live.LeaseExpires.After(now) {
			continue
		}
		registrations = append(registrations, live.Registration)
		replicas = append(replicas, models.TrafficRolloutReplica{
			ReplicaID: identity.ReplicaID, Incarnation: identity.IncarnationID,
			ReleaseSHA256: live.Registration.Release.SHA256, LeaseExpiresAt: live.LeaseExpires,
			Ready: live.Registration.Ready,
		})
	}
	if len(registrations) == 0 || !sdkmodels.ReleaseCohortCompatible(registrations) {
		return nil, models.PluginConfigurationConflict{}
	}
	return replicas, nil
}

// ValidateTrafficRolloutConfiguration validates an opaque candidate against
// every live ready replica's plugin-owned schema before Core promotes it.
func (resolver *RegisteredReplicaReloadResolver) ValidateTrafficRolloutConfiguration(ctx context.Context, instanceID string, document []byte) error {
	if resolver == nil || resolver.Source == nil || resolver.Build == nil || resolver.ValidateSchema == nil || instanceID == "" {
		return ErrPluginUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now := resolver.Now().UTC()
	validated := 0
	for _, live := range resolver.Source.Snapshot() {
		if live.Registration.Identity.InstanceID != instanceID || !live.Registration.Ready || !live.LeaseExpires.After(now) {
			continue
		}
		client, release, err := resolver.Build(ctx, live)
		if err != nil {
			return ErrPluginUnavailable
		}
		if release != nil {
			defer release()
		}
		schemaClient, ok := client.(pluginConfigurationSchemaClient)
		if !ok {
			return ErrPluginUnavailable
		}
		schema, err := schemaClient.ConfigurationSchema(ctx)
		if err != nil || resolver.ValidateSchema(document, schema) != nil {
			return models.PluginConfigurationConflict{}
		}
		validated++
	}
	if validated == 0 {
		return ErrPluginUnavailable
	}
	return nil
}

func NewRegisteredReplicaReloadResolver(source RegisteredReplicaSource, build RegisteredReplicaClientFactory, now func() time.Time) *RegisteredReplicaReloadResolver {
	if now == nil {
		now = time.Now
	}
	return &RegisteredReplicaReloadResolver{Source: source, Build: build, Now: now}
}

var _ applicationTrafficRolloutReplicaSource = (*RegisteredReplicaReloadResolver)(nil)
var _ applicationTrafficRolloutConfigurationValidator = (*RegisteredReplicaReloadResolver)(nil)

type applicationTrafficRolloutReplicaSource interface {
	TrafficRolloutReplicas(context.Context, string) ([]models.TrafficRolloutReplica, error)
}

type applicationTrafficRolloutConfigurationValidator interface {
	ValidateTrafficRolloutConfiguration(context.Context, string, []byte) error
}

// CaptureConfigurationTargets freezes each currently leased authenticated
// incarnation and release digest. Readiness is not used as a filter because a
// newly registered process must be able to receive its first config generation.
func (applier *SDKConfigurationApplier) CaptureConfigurationTargets(ctx context.Context, instanceID string) ([]models.PluginRolloutTarget, bool, error) {
	if applier == nil || applier.Registered == nil || applier.Registered.Source == nil || instanceID == "" {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if !applier.Registered.Source.HasRegisteredInstance(instanceID) {
		return nil, false, nil
	}
	now := applier.Registered.Now().UTC()
	targets := make([]models.PluginRolloutTarget, 0)
	cohort := make([]sdkmodels.ReplicaRegistrationRequest, 0)
	for _, replica := range applier.Registered.Source.Snapshot() {
		identity := replica.Registration.Identity
		if identity.InstanceID != instanceID || !replica.LeaseExpires.After(now) {
			continue
		}
		cohort = append(cohort, replica.Registration)
		target := models.PluginRolloutTarget{
			ReplicaID: identity.ReplicaID, IncarnationID: identity.IncarnationID,
			ReleaseSHA256: replica.Registration.Release.SHA256, LeaseExpiresAt: replica.LeaseExpires,
		}
		if !target.Valid() {
			return nil, true, ErrProtocolViolation
		}
		targets = append(targets, target)
	}
	if !sdkmodels.ReleaseCohortCompatible(cohort) {
		return nil, true, models.PluginConfigurationConflict{}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ReplicaID != targets[j].ReplicaID {
			return targets[i].ReplicaID < targets[j].ReplicaID
		}
		return targets[i].IncarnationID < targets[j].IncarnationID
	})
	return targets, true, nil
}

// LostConfigurationTargets identifies only targets with authoritative loss
// evidence: their frozen lease has expired, or the same logical replica has
// registered a different incarnation. Temporary transport failures and
// absence before lease expiry remain retryable and never substitute a target.
func (applier *SDKConfigurationApplier) LostConfigurationTargets(
	ctx context.Context,
	instanceID string,
	targets []models.PluginRolloutTarget,
) ([]models.PluginRolloutTarget, error) {
	if applier == nil || applier.Registered == nil || applier.Registered.Source == nil || instanceID == "" {
		return nil, ErrPluginUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := applier.Registered.Now().UTC()
	current := make(map[string]LivePluginReplica)
	for _, replica := range applier.Registered.Source.Snapshot() {
		identity := replica.Registration.Identity
		if identity.InstanceID == instanceID {
			current[identity.ReplicaID] = replica
		}
	}
	lost := make([]models.PluginRolloutTarget, 0)
	for _, target := range targets {
		if target.Acknowledged {
			continue
		}
		if !target.Valid() {
			return nil, ErrProtocolViolation
		}
		replica, hasReplica := current[target.ReplicaID]
		if hasReplica && replica.Registration.Identity.IncarnationID != target.IncarnationID {
			lost = append(lost, target)
			continue
		}
		if hasReplica && replica.Registration.Identity.IncarnationID == target.IncarnationID && replica.LeaseExpires.After(now) {
			continue
		}
		if !target.LeaseExpiresAt.After(now) {
			lost = append(lost, target)
		}
	}
	return lost, nil
}

// Resolve returns found=false only when this instance has never registered.
// That lets a not-yet-migrated legacy instance use its declared clients without
// allowing a known-but-expired remote instance to downgrade to that path.
func (resolver *RegisteredReplicaReloadResolver) Resolve(ctx context.Context, instanceID string) (SDKReloadClient, func(), bool, error) {
	if resolver == nil || resolver.Source == nil || resolver.Build == nil || instanceID == "" {
		return nil, nil, false, ErrPluginUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	now := resolver.Now().UTC()
	replicas := resolver.Source.Snapshot()
	eligible := make([]LivePluginReplica, 0, len(replicas))
	for _, replica := range replicas {
		if replica.Registration.Identity.InstanceID == instanceID && replica.LeaseExpires.After(now) {
			eligible = append(eligible, replica)
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].Registration.Identity.ReplicaID < eligible[j].Registration.Identity.ReplicaID
	})
	if len(eligible) == 0 {
		if resolver.Source.HasRegisteredInstance(instanceID) {
			return nil, nil, true, ErrPluginUnavailable
		}
		return nil, nil, false, nil
	}
	fanout := &SDKReloadFanout{InstanceID: instanceID, Replicas: make([]SDKReloadReplicaClient, 0, len(eligible))}
	releases := make([]func(), 0, len(eligible))
	closeAll := func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}
	for _, replica := range eligible {
		client, release, err := resolver.Build(ctx, replica)
		if err != nil || client == nil {
			if release != nil {
				release()
			}
			closeAll()
			return nil, nil, true, ErrPluginUnavailable
		}
		if release != nil {
			releases = append(releases, release)
		}
		fanout.Replicas = append(fanout.Replicas, SDKReloadReplicaClient{
			ReplicaID: replica.Registration.Identity.ReplicaID,
			Client:    client,
		})
	}
	var once sync.Once
	return fanout, func() { once.Do(closeAll) }, true, nil
}

// ResolveReplica returns one currently leased replica without widening the
// notification to its instance peers. It is used when a single incarnation
// registers or replaces itself so a scale-out event does not create an N²
// fan-out storm.
func (resolver *RegisteredReplicaReloadResolver) ResolveReplica(ctx context.Context, instanceID, replicaID string) (SDKReloadClient, func(), bool, error) {
	if resolver == nil || resolver.Source == nil || resolver.Build == nil || instanceID == "" || replicaID == "" {
		return nil, nil, false, ErrPluginUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	now := resolver.Now().UTC()
	for _, replica := range resolver.Source.Snapshot() {
		identity := replica.Registration.Identity
		if identity.InstanceID != instanceID || identity.ReplicaID != replicaID || !replica.LeaseExpires.After(now) {
			continue
		}
		client, release, err := resolver.Build(ctx, replica)
		if err != nil || client == nil {
			if release != nil {
				release()
			}
			return nil, nil, true, ErrPluginUnavailable
		}
		return client, release, true, nil
	}
	if resolver.Source.HasRegisteredInstance(instanceID) {
		return nil, nil, true, ErrPluginUnavailable
	}
	return nil, nil, false, nil
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
	if reload.Validate() != nil {
		return sdkmodels.ReloadAcknowledgement{}, nil, ErrProtocolViolation
	}
	results := make([]ReplicaReloadResult, len(fanout.Replicas))
	answers := make([]sdkmodels.ReloadAcknowledgement, len(fanout.Replicas))
	failures := make([]error, len(fanout.Replicas))
	var workers sync.WaitGroup
	for index, replica := range fanout.Replicas {
		workers.Add(1)
		go func(index int, replica SDKReloadReplicaClient) {
			defer workers.Done()
			if replica.Client == nil {
				results[index] = ReplicaReloadResult{ReplicaID: replica.ReplicaID, Unreachable: true}
				failures[index] = fmt.Errorf("replica %s/%s: %w", fanout.InstanceID, replica.ReplicaID, ErrPluginUnavailable)
				return
			}
			if readinessClient, ok := replica.Client.(SDKReplicaReadinessClient); ok {
				readiness, readinessErr := readinessClient.Readiness(ctx)
				if readinessErr == nil {
					if readiness.InstanceID != fanout.InstanceID || readiness.ReplicaID != replica.ReplicaID {
						failures[index] = fmt.Errorf("replica %s/%s: %w", fanout.InstanceID, replica.ReplicaID, ErrProtocolViolation)
						return
					}
					if reloadReadinessMatches(readiness, reload) {
						answers[index] = sdkmodels.ReloadAcknowledgement{
							Generation: reload.Generation, SHA256: reload.SHA256, SchemaVersion: reload.SchemaVersion,
							Applied: true, Outcome: sdkmodels.OutcomeAlreadyActive,
						}
						results[index] = ReplicaReloadResult{ReplicaID: replica.ReplicaID, Acknowledged: true}
						return
					}
				}
			}
			answer, err := replica.Client.Reload(ctx, reload)
			if err == nil && !reloadAcknowledgementMatches(reload, answer) {
				err = ErrProtocolViolation
			}
			if err != nil {
				results[index] = ReplicaReloadResult{ReplicaID: replica.ReplicaID, Unreachable: isUnreachable(err)}
				failures[index] = fmt.Errorf("replica %s/%s: %w", fanout.InstanceID, replica.ReplicaID, err)
				return
			}
			answers[index] = answer
			results[index] = ReplicaReloadResult{ReplicaID: replica.ReplicaID, Acknowledged: true}
		}(index, replica)
	}
	workers.Wait()
	if err := errors.Join(failures...); err != nil {
		return sdkmodels.ReloadAcknowledgement{}, results, err
	}
	return answers[0], results, nil
}

func reloadAcknowledgementMatches(request sdkmodels.Reload, acknowledgement sdkmodels.ReloadAcknowledgement) bool {
	if !acknowledgement.Applied || acknowledgement.Generation != request.Generation ||
		acknowledgement.SHA256 != request.SHA256 || acknowledgement.SchemaVersion != request.SchemaVersion {
		return false
	}
	return acknowledgement.Outcome == sdkmodels.OutcomeApplied || acknowledgement.Outcome == sdkmodels.OutcomeAlreadyActive
}

func reloadReadinessMatches(readiness sdkmodels.Readiness, reload sdkmodels.Reload) bool {
	return readiness.Ready && readiness.Generation == reload.Generation && readiness.SHA256 == reload.SHA256 &&
		readiness.SchemaVersion == reload.SchemaVersion
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
	Store                  interfaces.PluginConfigurationStore
	Clients                map[string]SDKReloadClient
	Registered             *RegisteredReplicaReloadResolver
	Observations           ReplicaObservationRecorder
	Snapshot               ConfigurationSnapshot
	Convergence            ConvergenceSnapshot
	SkipRegisteredInstance func(context.Context, string) (bool, error)
	applyMu                sync.Mutex
}

var _ interfaces.PluginConfigurationApplier = (*SDKConfigurationApplier)(nil)

func (applier *SDKConfigurationApplier) ApplyConfiguration(ctx context.Context, instanceID, generation string, rawJSON []byte) error {
	if applier == nil {
		return ErrPluginUnavailable
	}
	applier.applyMu.Lock()
	defer applier.applyMu.Unlock()
	if applier.Store == nil || instanceID == "" {
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
	client := applier.Clients[instanceID]
	var release func()
	registeredInstance := false
	if applier.Registered != nil {
		registeredClient, registeredRelease, found, resolveErr := applier.Registered.Resolve(ctx, instanceID)
		if resolveErr != nil {
			if found {
				return models.PluginConfigurationConvergencePending{}
			}
			return resolveErr
		}
		if found {
			registeredInstance = true
			client, release = registeredClient, registeredRelease
		}
	}
	if client == nil {
		return ErrPluginUnavailable
	}
	if release != nil {
		defer release()
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
		if fanoutErr != nil && registeredInstance {
			return models.PluginConfigurationConvergencePending{}
		}
		return fanoutErr
	}
	if fanout, isFanout := client.(*SDKReloadFanout); isFanout {
		_, _, err = fanout.ReloadObserved(ctx, reload)
	} else {
		_, err = client.Reload(ctx, reload)
	}
	if err != nil && registeredInstance {
		return models.PluginConfigurationConvergencePending{}
	}
	return err
}

// ApplyConfigurationToTargets reconciles only the persisted rollout cohort.
// Current directory membership is used to find the transport for an exact
// identity, never to add a replacement incarnation to the cohort.
func (applier *SDKConfigurationApplier) ApplyConfigurationToTargets(
	ctx context.Context,
	operationID, instanceID, generation string,
	rawJSON []byte,
	targets []models.PluginRolloutTarget,
) ([]models.PluginRolloutTarget, error) {
	if applier == nil || applier.Store == nil || applier.Registered == nil || applier.Registered.Source == nil ||
		operationID == "" || instanceID == "" || len(targets) == 0 {
		return nil, models.PluginConfigurationConvergencePending{}
	}
	generationNumber, err := strconv.ParseInt(generation, 10, 64)
	if err != nil || generationNumber < 1 || strconv.FormatInt(generationNumber, 10) != generation {
		return nil, ErrProtocolViolation
	}
	revision, _, err := applier.Store.Current(ctx, instanceID)
	if err != nil || revision.Revision != generationNumber || !bytes.Equal(revision.SettingsJSON, rawJSON) {
		return nil, ErrProtocolViolation
	}
	applier.applyMu.Lock()
	defer applier.applyMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if applier.Convergence != nil {
		applier.Convergence.Invalidate()
	}
	if applier.Snapshot != nil {
		if err := applier.Snapshot.Refresh(context.WithoutCancel(ctx), instanceID); err != nil {
			return nil, err
		}
	}
	if applier.Convergence != nil {
		if err := applier.Convergence.Refresh(context.WithoutCancel(ctx)); err != nil {
			return nil, err
		}
	}

	live := make(map[string]LivePluginReplica)
	now := applier.Registered.Now().UTC()
	for _, replica := range applier.Registered.Source.Snapshot() {
		identity := replica.Registration.Identity
		if identity.InstanceID != instanceID || !replica.LeaseExpires.After(now) {
			continue
		}
		live[identity.ReplicaID+"\x00"+identity.IncarnationID] = replica
	}

	clients := make([]SDKReloadReplicaClient, 0, len(targets))
	clientTargets := make(map[string]models.PluginRolloutTarget, len(targets))
	var failures []error
	for _, target := range targets {
		if target.Acknowledged {
			continue
		}
		if !target.Valid() {
			return nil, ErrProtocolViolation
		}
		key := target.ReplicaID + "\x00" + target.IncarnationID
		replica, exists := live[key]
		if !exists || replica.Registration.Release.SHA256 != target.ReleaseSHA256 {
			failures = append(failures, fmt.Errorf("replica %s/%s: %w", target.ReplicaID, target.IncarnationID, ErrPluginUnavailable))
			continue
		}
		client, release, buildErr := applier.Registered.Build(ctx, replica)
		if release != nil {
			defer release()
		}
		if buildErr != nil || client == nil {
			if buildErr == nil {
				buildErr = ErrPluginUnavailable
			}
			failures = append(failures, fmt.Errorf("replica %s/%s: %w", target.ReplicaID, target.IncarnationID, buildErr))
			continue
		}
		clients = append(clients, SDKReloadReplicaClient{ReplicaID: target.ReplicaID, Client: client})
		clientTargets[target.ReplicaID] = target
	}
	var acknowledged []models.PluginRolloutTarget
	if len(clients) > 0 {
		fanout := &SDKReloadFanout{InstanceID: instanceID, Replicas: clients}
		_, results, fanoutErr := fanout.ReloadObserved(ctx, sdkmodels.Reload{
			Generation: generation, SHA256: revision.Digest, SchemaVersion: strconv.FormatInt(revision.SchemaVersion, 10),
		})
		for _, result := range results {
			target, found := clientTargets[result.ReplicaID]
			if !found {
				continue
			}
			applier.recordOutcome(ctx, instanceID, result.ReplicaID, generationNumber, result.Acknowledged, result.Unreachable)
			if result.Acknowledged {
				target.Acknowledged = true
				acknowledged = append(acknowledged, target)
			}
		}
		if fanoutErr != nil {
			failures = append(failures, fanoutErr)
		}
	}
	if len(failures) != 0 || len(acknowledged) != len(clients) {
		return acknowledged, models.PluginConfigurationConvergencePending{}
	}
	return acknowledged, nil
}

// ReloadActive announces the already-committed active generation after a new
// replica registers. The replica then pulls that exact generation through the
// private SDK endpoint; Core never includes plugin configuration in Reload.
func (applier *SDKConfigurationApplier) ReloadActive(ctx context.Context, instanceID string) error {
	if applier == nil || applier.Store == nil || instanceID == "" {
		return ErrPluginUnavailable
	}
	active, _, err := applier.Store.Current(ctx, instanceID)
	if err != nil {
		if errors.Is(err, models.PluginConfigurationNotFound{}) {
			return nil
		}
		return err
	}
	if active.Revision < 1 {
		return nil
	}
	return applier.ApplyConfiguration(ctx, instanceID, strconv.FormatInt(active.Revision, 10), active.SettingsJSON)
}
