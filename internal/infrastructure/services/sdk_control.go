package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
)

type SDKReloadClient interface {
	Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error)
}

// SDKReloadFanout announces one generation concurrently to every live replica
// in a resolved lease snapshot. Results retain the snapshot order.
type SDKReloadFanout struct {
	InstanceID string
	Replicas   []SDKReloadReplicaClient
}

// SDKReloadReplicaClient is one authenticated replica's control client and id.
type SDKReloadReplicaClient struct {
	ReplicaID     string
	IncarnationID string
	Release       sdkmodels.ReplicaRelease
	Client        SDKReloadClient
}

// RegisteredReplicaSource exposes the current authenticated lease snapshot and
// remembers whether an instance has ever registered during this Core process.
// The history bit prevents an expired lease from being mistaken for a new one.
type RegisteredReplicaSource interface {
	Snapshot() []LiveServiceReplica
	HasRegisteredInstance(string) bool
}

type RegisteredReplicaClientFactory func(context.Context, LiveServiceReplica) (SDKReloadClient, func(), error)

// RegisteredReplicaReloadResolver builds a fresh control fanout from one live
// directory snapshot for each configuration activation. It does not persist
// endpoints or replay work; the directory is the authority for eligible leases.
type RegisteredReplicaReloadResolver struct {
	Source RegisteredReplicaSource
	Build  RegisteredReplicaClientFactory
	Now    func() time.Time
	// OnMetadataFailure reports a bounded, SDK-owned failure description to the
	// Core operator log. It must never forward endpoint, credential, or payload
	// values to the Management API response.
	OnMetadataFailure func(instanceID, phase string, err error)
	// ValidateSchema is supplied by the composition root; no configuration
	// loader is reachable from the service transport adapter.
	ValidateSchema func(document, schema []byte) error
}

type serviceConfigurationSchemaClient interface {
	ConfigurationSchema(context.Context) ([]byte, error)
}

type serviceConfigurationValidationClient interface {
	ValidateConfiguration(context.Context, string, []byte) error
}

type sdkMetadataClient interface {
	Manifest(context.Context) ([]byte, error)
	ConfigurationSchema(context.Context) ([]byte, error)
	Identity(context.Context) (sdkmodels.Registration, error)
	Readiness(context.Context) (sdkmodels.Readiness, error)
}

// ReplicaMetadata is the Core-owned, read-only view used by Management API
// adapters. The calls below are the only place Core reaches live Service SDK
// REST metadata; callers such as CLI never receive a service client.
type ReplicaMetadata struct {
	Manifest            []byte
	ConfigurationSchema []byte
	Identity            sdkmodels.Registration
	Readiness           sdkmodels.Readiness
	ReplicaID           string
	Release             sdkmodels.ReplicaRelease
	Replicas            []ReplicaReleaseMetadata
}

// ReplicaReleaseMetadata describes the release claim for one live authenticated
// lease. Metadata reads fail closed if a service has mixed live releases.
type ReplicaReleaseMetadata struct {
	ReplicaID     string
	IncarnationID string
	Release       sdkmodels.ReplicaRelease
	Identity      sdkmodels.Registration
	Readiness     sdkmodels.Readiness
}

func (resolver *RegisteredReplicaReloadResolver) Metadata(ctx context.Context, instanceID string) (ReplicaMetadata, error) {
	fail := func(phase string, err error) (ReplicaMetadata, error) {
		if resolver.OnMetadataFailure != nil {
			resolver.OnMetadataFailure(instanceID, phase, err)
		}
		return ReplicaMetadata{}, err
	}
	client, release, found, err := resolver.Resolve(ctx, instanceID)
	if release != nil {
		defer release()
	}
	if err != nil || !found {
		return fail("resolve", ErrServiceUnavailable)
	}
	fanout, ok := client.(*SDKReloadFanout)
	if !ok || len(fanout.Replicas) == 0 {
		return fail("resolve", ErrServiceUnavailable)
	}
	replicas := make([]ReplicaReleaseMetadata, 0, len(fanout.Replicas))
	for _, replica := range fanout.Replicas {
		if replica.ReplicaID == "" || !replica.Release.Valid() {
			return fail("release", ErrServiceUnavailable)
		}
		if len(replicas) > 0 && (replica.Release.Version != replicas[0].Release.Version || replica.Release.SHA256 != replicas[0].Release.SHA256) {
			return fail("release", models.ErrServiceRegistrationMismatch)
		}
		replicas = append(replicas, ReplicaReleaseMetadata{ReplicaID: replica.ReplicaID, IncarnationID: replica.IncarnationID, Release: replica.Release})
	}
	metadataClient, ok := fanout.Replicas[0].Client.(sdkMetadataClient)
	if !ok {
		return fail("resolve", ErrServiceUnavailable)
	}
	var metadata ReplicaMetadata
	metadata.ReplicaID = fanout.Replicas[0].ReplicaID
	metadata.Release = fanout.Replicas[0].Release
	metadata.Replicas = replicas
	if metadata.ReplicaID == "" || !metadata.Release.Valid() {
		return fail("release", ErrServiceUnavailable)
	}
	// Core owns these four live SDK REST reads. Run them concurrently so one
	// Management request stays within the SDK read deadline instead of spending
	// that deadline serially on manifest, schema, identity, and readiness.
	metadataContext, cancel := context.WithCancel(ctx)
	defer cancel()
	type metadataResult struct {
		kind      string
		manifest  []byte
		schema    []byte
		identity  sdkmodels.Registration
		readiness sdkmodels.Readiness
		err       error
	}
	results := make(chan metadataResult, 4)
	go func() {
		value, err := metadataClient.Manifest(metadataContext)
		results <- metadataResult{kind: "manifest", manifest: value, err: err}
	}()
	go func() {
		value, err := metadataClient.ConfigurationSchema(metadataContext)
		results <- metadataResult{kind: "schema", schema: value, err: err}
	}()
	go func() {
		value, err := metadataClient.Identity(metadataContext)
		results <- metadataResult{kind: "identity", identity: value, err: err}
	}()
	go func() {
		value, err := metadataClient.Readiness(metadataContext)
		results <- metadataResult{kind: "readiness", readiness: value, err: err}
	}()
	var firstErr error
	firstFailurePhase := ""
	for range 4 {
		result := <-results
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
				firstFailurePhase = result.kind
				cancel()
			}
			continue
		}
		switch result.kind {
		case "manifest":
			metadata.Manifest = append([]byte(nil), result.manifest...)
		case "schema":
			metadata.ConfigurationSchema = append([]byte(nil), result.schema...)
		case "identity":
			metadata.Identity = result.identity
		case "readiness":
			metadata.Readiness = result.readiness
		}
	}
	if firstErr != nil {
		return fail(firstFailurePhase, firstErr)
	}
	if metadata.Identity.InstanceID != instanceID || metadata.Identity.ReplicaID != metadata.ReplicaID ||
		metadata.Readiness.InstanceID != instanceID || metadata.Readiness.ReplicaID != metadata.ReplicaID {
		return fail("identity", models.ErrServiceRegistrationMismatch)
	}
	metadata.Replicas[0].Identity = metadata.Identity
	metadata.Replicas[0].Readiness = metadata.Readiness
	for index := 1; index < len(fanout.Replicas); index++ {
		replica := fanout.Replicas[index]
		client, ok := replica.Client.(sdkMetadataClient)
		if !ok {
			return fail("resolve", ErrServiceUnavailable)
		}
		identity, readiness, phase, err := readReplicaStatus(ctx, client)
		if err != nil {
			return fail(phase, err)
		}
		if identity.InstanceID != instanceID || identity.ReplicaID != replica.ReplicaID ||
			readiness.InstanceID != instanceID || readiness.ReplicaID != replica.ReplicaID {
			return fail("identity", models.ErrServiceRegistrationMismatch)
		}
		metadata.Replicas[index].Identity = identity
		metadata.Replicas[index].Readiness = readiness
	}
	return metadata, nil
}

func readReplicaStatus(ctx context.Context, client sdkMetadataClient) (sdkmodels.Registration, sdkmodels.Readiness, string, error) {
	type result struct {
		identity  sdkmodels.Registration
		readiness sdkmodels.Readiness
		kind      string
		err       error
	}
	results := make(chan result, 2)
	go func() {
		value, err := client.Identity(ctx)
		results <- result{identity: value, kind: "identity", err: err}
	}()
	go func() {
		value, err := client.Readiness(ctx)
		results <- result{readiness: value, kind: "readiness", err: err}
	}()
	var identity sdkmodels.Registration
	var readiness sdkmodels.Readiness
	for range 2 {
		item := <-results
		if item.err != nil {
			return sdkmodels.Registration{}, sdkmodels.Readiness{}, item.kind, item.err
		}
		if item.kind == "identity" {
			identity = item.identity
		} else {
			readiness = item.readiness
		}
	}
	return identity, readiness, "", nil
}

// ValidateRuntimeRegistration binds the CLI's locally verified artifact claim
// to the exact currently leased replica that independently authenticated with
// Core through the Service SDK. The SDK release digest is a replica claim, not
// standalone proof of binary provenance; equality prevents accidental or
// stale artifact/endpoint registration across the two control-plane paths.
func (resolver *RegisteredReplicaReloadResolver) ValidateRuntimeRegistration(ctx context.Context, claim models.ServiceRegistration) error {
	if resolver == nil || resolver.Source == nil || claim.ArtifactID == "" || claim.InstanceID == "" || claim.ReplicaID == "" {
		return ErrServiceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if claim.ArtifactID != claim.InstanceID {
		return models.ErrServiceRegistrationMismatch
	}
	now := resolver.Now().UTC()
	instanceFound := false
	for _, replica := range resolver.Source.Snapshot() {
		identity := replica.Registration.Identity
		if identity.InstanceID != claim.InstanceID || !replica.LeaseExpires.After(now) {
			continue
		}
		instanceFound = true
		if identity.ReplicaID != claim.ReplicaID {
			continue
		}
		registered := replica.Registration
		if registered.Release.Version != claim.ArtifactVersion ||
			"sha256:"+registered.Release.SHA256 != claim.Digest ||
			registered.RestEndpoint != claim.ControlEndpoint ||
			!runtimePeerEndpointMatches(claim.PeerEndpoint, registered.PeerEndpoints) {
			return models.ErrServiceRegistrationMismatch
		}
		return nil
	}
	if instanceFound {
		return models.ErrServiceRegistrationMismatch
	}
	return ErrServiceUnavailable
}

func runtimePeerEndpointMatches(claim string, registered []sdkmodels.ReplicaPeerEndpoint) bool {
	// A service that only initiates peer calls has no advertised listener. The
	// CLI may still use a local peer bind setting for that process, so require a
	// match only when the Service SDK actually advertises peer endpoints.
	if len(registered) == 0 {
		return true
	}
	if claim == "" {
		return false
	}
	parsed, err := url.ParseRequestURI(claim)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || len(registered) != 1 {
		return false
	}
	endpoint := registered[0]
	return (endpoint.Carrier == sdkmodels.PeerCarrierTCP || endpoint.Carrier == sdkmodels.PeerCarrierQUIC) && endpoint.Endpoint == parsed.Host
}

// TrafficRolloutReplicas adapts authenticated Service SDK registrations into the
// neutral cohort model. Compatibility remains defined by the SDK's generic
// release contract, not by any product service.
func (resolver *RegisteredReplicaReloadResolver) TrafficRolloutReplicas(ctx context.Context, instanceID string) ([]models.TrafficRolloutReplica, error) {
	if resolver == nil || resolver.Source == nil || instanceID == "" {
		return nil, ErrServiceUnavailable
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
		return nil, models.ServiceConfigurationConflict{}
	}
	return replicas, nil
}

// TrafficRolloutReplicas exposes the authenticated REST registration cohort.
func (applier *SDKConfigurationApplier) TrafficRolloutReplicas(ctx context.Context, instanceID string) ([]models.TrafficRolloutReplica, error) {
	if applier == nil || instanceID == "" {
		return nil, ErrServiceUnavailable
	}
	if applier.Registered == nil {
		return nil, ErrServiceUnavailable
	}
	return applier.Registered.TrafficRolloutReplicas(ctx, instanceID)
}

// ValidateTrafficRolloutConfiguration validates an opaque candidate against
// every live replica's service-owned schema and validator before Core promotes it.
func (resolver *RegisteredReplicaReloadResolver) ValidateTrafficRolloutConfiguration(ctx context.Context, instanceID, generation string, document []byte) error {
	if resolver == nil || resolver.Source == nil || resolver.Build == nil || resolver.ValidateSchema == nil || instanceID == "" {
		return ErrServiceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now := resolver.Now().UTC()
	validated := 0
	for _, live := range resolver.Source.Snapshot() {
		if live.Registration.Identity.InstanceID != instanceID || !live.LeaseExpires.After(now) {
			continue
		}
		client, release, err := resolver.Build(ctx, live)
		if err != nil {
			return ErrServiceUnavailable
		}
		if release != nil {
			defer release()
		}
		schemaClient, ok := client.(serviceConfigurationSchemaClient)
		if !ok {
			return ErrServiceUnavailable
		}
		schema, err := schemaClient.ConfigurationSchema(ctx)
		if err != nil || resolver.ValidateSchema(document, schema) != nil {
			return models.ServiceConfigurationConflict{}
		}
		validator, ok := client.(serviceConfigurationValidationClient)
		if !ok {
			return ErrServiceUnavailable
		}
		if err := validator.ValidateConfiguration(ctx, generation, document); err != nil {
			return models.ServiceConfigurationConflict{}
		}
		validated++
	}
	if validated == 0 {
		return ErrServiceUnavailable
	}
	return nil
}

func (applier *SDKConfigurationApplier) ValidateTrafficRolloutConfiguration(ctx context.Context, instanceID, generation string, document []byte) error {
	if applier == nil || instanceID == "" {
		return ErrServiceUnavailable
	}
	if applier.Registered == nil {
		return ErrServiceUnavailable
	}
	return applier.Registered.ValidateTrafficRolloutConfiguration(ctx, instanceID, generation, document)
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
	ValidateTrafficRolloutConfiguration(context.Context, string, string, []byte) error
}

// CaptureConfigurationTargets freezes each currently leased authenticated
// incarnation and release digest. Readiness is not used as a filter because a
// newly registered process must be able to receive its first config generation.
func (applier *SDKConfigurationApplier) CaptureConfigurationTargets(ctx context.Context, instanceID string) ([]models.ServiceRolloutTarget, bool, error) {
	if applier == nil || instanceID == "" {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if applier.Registered == nil || applier.Registered.Source == nil {
		return nil, false, nil
	}
	if !applier.Registered.Source.HasRegisteredInstance(instanceID) {
		return nil, false, nil
	}
	now := applier.Registered.Now().UTC()
	targets := make([]models.ServiceRolloutTarget, 0)
	cohort := make([]sdkmodels.ReplicaRegistrationRequest, 0)
	for _, replica := range applier.Registered.Source.Snapshot() {
		identity := replica.Registration.Identity
		if identity.InstanceID != instanceID || !replica.LeaseExpires.After(now) {
			continue
		}
		cohort = append(cohort, replica.Registration)
		target := models.ServiceRolloutTarget{
			ReplicaID: identity.ReplicaID, IncarnationID: identity.IncarnationID,
			ReleaseSHA256: replica.Registration.Release.SHA256, LeaseExpiresAt: replica.LeaseExpires,
		}
		if !target.Valid() {
			return nil, true, ErrProtocolViolation
		}
		targets = append(targets, target)
	}
	if !sdkmodels.ReleaseCohortCompatible(cohort) {
		return nil, true, models.ServiceConfigurationConflict{}
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
	targets []models.ServiceRolloutTarget,
) ([]models.ServiceRolloutTarget, error) {
	if applier == nil || instanceID == "" {
		return nil, ErrServiceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if applier.Registered == nil || applier.Registered.Source == nil {
		return nil, ErrServiceUnavailable
	}
	now := applier.Registered.Now().UTC()
	current := make(map[string]LiveServiceReplica)
	for _, replica := range applier.Registered.Source.Snapshot() {
		identity := replica.Registration.Identity
		if identity.InstanceID == instanceID {
			current[identity.ReplicaID] = replica
		}
	}
	lost := make([]models.ServiceRolloutTarget, 0)
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
// Previously registered but expired instances remain unavailable until a new
// authenticated lease is admitted.
func (resolver *RegisteredReplicaReloadResolver) Resolve(ctx context.Context, instanceID string) (SDKReloadClient, func(), bool, error) {
	if resolver == nil || resolver.Source == nil || resolver.Build == nil || instanceID == "" {
		return nil, nil, false, ErrServiceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	now := resolver.Now().UTC()
	replicas := resolver.Source.Snapshot()
	eligible := make([]LiveServiceReplica, 0, len(replicas))
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
			return nil, nil, true, ErrServiceUnavailable
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
			return nil, nil, true, ErrServiceUnavailable
		}
		if release != nil {
			releases = append(releases, release)
		}
		fanout.Replicas = append(fanout.Replicas, SDKReloadReplicaClient{
			ReplicaID: replica.Registration.Identity.ReplicaID, IncarnationID: replica.Registration.Identity.IncarnationID,
			Release: replica.Registration.Release, Client: client,
		})
	}
	var once sync.Once
	return fanout, func() { once.Do(closeAll) }, true, nil
}

// ResolveReplica returns one currently leased replica without widening the
// notification to its instance peers. It is used when a single incarnation
// registers or replaces itself so a scale-out event does not create an N²
// fan-out storm.
func (resolver *RegisteredReplicaReloadResolver) ResolveReplica(ctx context.Context, instanceID, replicaID string) (SDKReloadClient, func(), string, bool, error) {
	if resolver == nil || resolver.Source == nil || resolver.Build == nil || instanceID == "" || replicaID == "" {
		return nil, nil, "", false, ErrServiceUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, "", false, err
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
			return nil, nil, "", true, ErrServiceUnavailable
		}
		return client, release, identity.IncarnationID, true, nil
	}
	if resolver.Source.HasRegisteredInstance(instanceID) {
		return nil, nil, "", true, ErrServiceUnavailable
	}
	return nil, nil, "", false, nil
}

// Reload announces the generation to every replica in this resolved snapshot.
// Its returned error identifies failed replicas. Calls are never replayed here: whether to retry an
// announcement is a Core operation decision, not a client one.
func (fanout *SDKReloadFanout) Reload(ctx context.Context, reload sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	acknowledged, _, err := fanout.ReloadObserved(ctx, reload)
	return acknowledged, err
}

// ReplicaReloadResult is the per-replica outcome of one announcement. Core
// records these as observations so readiness and drift can be derived from what
// each live replica actually did.
type ReplicaReloadResult struct {
	ReplicaID     string
	IncarnationID string
	Acknowledged  bool
	// Unreachable is set when the replica could not be contacted at all, which is
	// a different operator-visible condition from a replica that was reached and
	// refused the generation.
	Unreachable bool
}

// ReloadObserved announces the generation to every resolved replica and reports
// each outcome. Failed replicas remain fenced while successfully acknowledged
// replicas may serve the desired generation.
func (fanout *SDKReloadFanout) ReloadObserved(ctx context.Context, reload sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, []ReplicaReloadResult, error) {
	if fanout == nil || len(fanout.Replicas) == 0 {
		return sdkmodels.ReloadAcknowledgement{}, nil, ErrServiceUnavailable
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
			results[index] = ReplicaReloadResult{ReplicaID: replica.ReplicaID, IncarnationID: replica.IncarnationID}
			if replica.Client == nil {
				results[index] = ReplicaReloadResult{ReplicaID: replica.ReplicaID, IncarnationID: replica.IncarnationID, Unreachable: true}
				failures[index] = fmt.Errorf("replica %s/%s: %w", fanout.InstanceID, replica.ReplicaID, ErrServiceUnavailable)
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
						results[index] = ReplicaReloadResult{ReplicaID: replica.ReplicaID, IncarnationID: replica.IncarnationID, Acknowledged: true}
						return
					}
				}
			}
			answer, err := replica.Client.Reload(ctx, reload)
			if err == nil && !reloadAcknowledgementMatches(reload, answer) {
				err = ErrProtocolViolation
			}
			if err != nil {
				results[index] = ReplicaReloadResult{ReplicaID: replica.ReplicaID, IncarnationID: replica.IncarnationID, Unreachable: isUnreachable(err)}
				failures[index] = fmt.Errorf("replica %s/%s: %w", fanout.InstanceID, replica.ReplicaID, err)
				return
			}
			answers[index] = answer
			results[index] = ReplicaReloadResult{ReplicaID: replica.ReplicaID, IncarnationID: replica.IncarnationID, Acknowledged: true}
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
	return errors.Is(err, ErrServiceUnavailable)
}

// ReplicaObservationRecorder records the latest outcome for an authenticated
// replica. Observations do not extend a lease or change registered identity.
type ReplicaObservationRecorder interface {
	RecordReplicaObservation(ctx context.Context, instanceID, replicaID, incarnationID string, generation int64, acknowledged, unreachable bool)
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
	Store                  interfaces.ServiceConfigurationStore
	Registered             *RegisteredReplicaReloadResolver
	Observations           ReplicaObservationRecorder
	Snapshot               ConfigurationSnapshot
	Convergence            ConvergenceSnapshot
	SkipRegisteredInstance func(context.Context, string) (bool, error)
	applyMu                sync.Mutex
}

var _ interfaces.ServiceConfigurationApplier = (*SDKConfigurationApplier)(nil)

func (applier *SDKConfigurationApplier) ApplyConfiguration(ctx context.Context, instanceID, generation string, rawJSON []byte) error {
	if applier == nil {
		return ErrServiceUnavailable
	}
	applier.applyMu.Lock()
	defer applier.applyMu.Unlock()
	if applier.Store == nil || instanceID == "" {
		return ErrServiceUnavailable
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
	client, release, found, resolveErr := applier.resolve(ctx, instanceID)
	if resolveErr != nil {
		if found {
			return models.ServiceConfigurationConvergencePending{}
		}
		return resolveErr
	}
	if !found || client == nil {
		return ErrServiceUnavailable
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
	// Record every live registration outcome so readiness reflects the exact
	// authenticated replicas that converged rather than one representative.
	if fanout, isFanout := client.(*SDKReloadFanout); isFanout && applier.Observations != nil {
		_, results, fanoutErr := fanout.ReloadObserved(ctx, reload)
		for _, result := range results {
			applier.Observations.RecordReplicaObservation(ctx, instanceID, result.ReplicaID, result.IncarnationID, generationNumber, result.Acknowledged, result.Unreachable)
		}
		if fanoutErr != nil {
			return models.ServiceConfigurationConvergencePending{}
		}
		return fanoutErr
	}
	if fanout, isFanout := client.(*SDKReloadFanout); isFanout {
		_, _, err = fanout.ReloadObserved(ctx, reload)
	} else {
		_, err = client.Reload(ctx, reload)
	}
	if err != nil {
		return models.ServiceConfigurationConvergencePending{}
	}
	return err
}

func (applier *SDKConfigurationApplier) resolve(ctx context.Context, instanceID string) (SDKReloadClient, func(), bool, error) {
	if applier == nil || instanceID == "" {
		return nil, nil, false, ErrServiceUnavailable
	}
	if applier.Registered == nil {
		return nil, nil, false, ErrServiceUnavailable
	}
	return applier.Registered.Resolve(ctx, instanceID)
}

// ApplyConfigurationToTargets reconciles only the persisted rollout cohort.
// Current directory membership is used to find the transport for an exact
// identity, never to add a replacement incarnation to the cohort.
func (applier *SDKConfigurationApplier) ApplyConfigurationToTargets(
	ctx context.Context,
	operationID, instanceID, generation string,
	rawJSON []byte,
	targets []models.ServiceRolloutTarget,
) ([]models.ServiceRolloutTarget, error) {
	if applier == nil || applier.Store == nil ||
		applier.Registered == nil ||
		operationID == "" || instanceID == "" || len(targets) == 0 {
		return nil, models.ServiceConfigurationConvergencePending{}
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

	clients := make([]SDKReloadReplicaClient, 0, len(targets))
	clientTargets := make(map[string]models.ServiceRolloutTarget, len(targets))
	var failures []error

	if applier.Registered == nil || applier.Registered.Source == nil {
		return nil, models.ServiceConfigurationConvergencePending{}
	}
	live := make(map[string]LiveServiceReplica)
	now := applier.Registered.Now().UTC()
	for _, replica := range applier.Registered.Source.Snapshot() {
		identity := replica.Registration.Identity
		if identity.InstanceID != instanceID || !replica.LeaseExpires.After(now) {
			continue
		}
		live[identity.ReplicaID+"\x00"+identity.IncarnationID] = replica
	}
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
			failures = append(failures, fmt.Errorf("replica %s/%s: %w", target.ReplicaID, target.IncarnationID, ErrServiceUnavailable))
			continue
		}
		client, release, buildErr := applier.Registered.Build(ctx, replica)
		if release != nil {
			defer release()
		}
		if buildErr != nil || client == nil {
			if buildErr == nil {
				buildErr = ErrServiceUnavailable
			}
			failures = append(failures, fmt.Errorf("replica %s/%s: %w", target.ReplicaID, target.IncarnationID, buildErr))
			continue
		}
		clients = append(clients, SDKReloadReplicaClient{ReplicaID: target.ReplicaID, IncarnationID: target.IncarnationID, Client: client})
		clientTargets[target.ReplicaID] = target
	}

	var acknowledged []models.ServiceRolloutTarget
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
			applier.recordOutcome(ctx, instanceID, result.ReplicaID, result.IncarnationID, generationNumber, result.Acknowledged, result.Unreachable)
			if result.Acknowledged {
				target.Acknowledged = true
				acknowledged = append(acknowledged, target)
			}
		}
		if fanoutErr != nil {
			allUnreachable := true
			for _, result := range results {
				if !result.Acknowledged && !result.Unreachable {
					allUnreachable = false
					break
				}
			}
			if !allUnreachable {
				return acknowledged, fanoutErr
			}
			failures = append(failures, fanoutErr)
		}
	}
	if len(failures) != 0 || len(acknowledged) != len(clients) {
		return acknowledged, models.ServiceConfigurationConvergencePending{}
	}
	return acknowledged, nil
}

// ReloadActive announces the already-committed active generation after a new
// replica registers. The replica then pulls that exact generation through the
// private SDK endpoint; Core never includes service configuration in Reload.
func (applier *SDKConfigurationApplier) ReloadActive(ctx context.Context, instanceID string) error {
	if applier == nil || applier.Store == nil || instanceID == "" {
		return ErrServiceUnavailable
	}
	active, _, err := applier.Store.Current(ctx, instanceID)
	if err != nil {
		if errors.Is(err, models.ServiceConfigurationNotFound{}) {
			return nil
		}
		return err
	}
	if active.Revision < 1 {
		return nil
	}
	return applier.ApplyConfiguration(ctx, instanceID, strconv.FormatInt(active.Revision, 10), active.SettingsJSON)
}
