package plugins

import (
	"context"
	"crypto/x509"
	"sort"
	"sync"
	"time"

	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"
)

// LivePluginReplica is the authenticated, in-memory description of one active
// plugin replica incarnation. Lease and endpoint state is intentionally lost
// when Core restarts; replicas must register again before becoming eligible.
type LivePluginReplica struct {
	Registration sdkmodels.ReplicaRegistrationRequest
	LeaseExpires time.Time
}

// PluginReplicaDirectory tracks currently leased plugin replicas. It contains
// transport metadata only and has no plugin-specific behavior.
type PluginReplicaDirectory struct {
	mu        sync.RWMutex
	contract  sdkinfrastructure.ReplicaLifecycleContract
	now       func() time.Time
	entries   map[replicaDirectoryKey]LivePluginReplica
	instances map[string]struct{}
	notify    func()
}

type replicaDirectoryKey struct {
	instanceID string
	replicaID  string
}

// NewPluginReplicaDirectory creates an empty live directory using the lease
// duration from the Plugin SDK owner contract.
func NewPluginReplicaDirectory(contract sdkinfrastructure.ReplicaLifecycleContract, now func() time.Time) (*PluginReplicaDirectory, error) {
	if err := contract.Validate(); err != nil || contract.Lease.TTLSeconds <= 0 {
		return nil, ErrPluginUnavailable
	}
	if now == nil {
		now = time.Now
	}
	return &PluginReplicaDirectory{
		contract:  contract,
		now:       now,
		entries:   make(map[replicaDirectoryKey]LivePluginReplica),
		instances: make(map[string]struct{}),
	}, nil
}

// SetChangeNotifier installs a callback invoked after any successful membership
// change so pending peer-directory long-poll waiters are woken. The callback is
// only ever read while the directory lock is held.
func (directory *PluginReplicaDirectory) SetChangeNotifier(notify func()) {
	if directory == nil {
		return
	}
	directory.mu.Lock()
	directory.notify = notify
	directory.mu.Unlock()
}

// Register admits only a verified certificate whose exact URI SAN is the
// identity declared in the request. The HTTP adapter must require a verified
// client certificate before calling this method.
func (directory *PluginReplicaDirectory) Register(request sdkmodels.ReplicaRegistrationRequest, certificate *x509.Certificate) (sdkmodels.ReplicaRegistrationResponse, error) {
	return directory.RegisterAndPersist(context.Background(), request, certificate, nil)
}

// RegisterAndPersist serializes durable admission and live publication for one
// replica key. The callback runs while the directory write lock is held, so a
// concurrent duplicate cannot become visible and later be removed by rollback
// of a failed sibling registration.
func (directory *PluginReplicaDirectory) RegisterAndPersist(
	ctx context.Context,
	request sdkmodels.ReplicaRegistrationRequest,
	certificate *x509.Certificate,
	persist func(context.Context, sdkmodels.ReplicaRegistrationRequest) error,
) (sdkmodels.ReplicaRegistrationResponse, error) {
	if directory == nil || request.Validate() != nil || request.ContractVersion != directory.contract.ContractVersion ||
		len(request.PeerEndpoints) > directory.contract.Limits.MaximumPeerEndpoints ||
		len(request.AdvertisedContracts) > directory.contract.Limits.MaximumContractsPerList ||
		len(request.AcceptedContracts) > directory.contract.Limits.MaximumContractsPerList {
		return sdkmodels.ReplicaRegistrationResponse{}, sdkmodels.ErrInvalidReplicaRegistration
	}
	if !directory.certificateMatches(certificate, request.Identity) {
		return sdkmodels.ReplicaRegistrationResponse{}, sdkmodels.ErrReplicaIdentityMismatch
	}
	_, err := directory.contract.ReplicaIdentityURI(request.Identity)
	if err != nil {
		return sdkmodels.ReplicaRegistrationResponse{}, sdkmodels.ErrInvalidReplicaRegistration
	}

	now := directory.now().UTC()
	key := replicaDirectoryKey{instanceID: request.Identity.InstanceID, replicaID: request.Identity.ReplicaID}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	changed := false
	defer func() {
		if changed && directory.notify != nil {
			directory.notify()
		}
	}()
	if previous, exists := directory.entries[key]; exists {
		if previous.LeaseExpires.After(now) {
			if previous.Registration.Identity != request.Identity || !sameImmutableRegistration(previous.Registration, request) {
				return sdkmodels.ReplicaRegistrationResponse{}, sdkmodels.ErrReplicaIdentityConflict
			}
		} else if previous.Registration.Identity.IncarnationID == request.Identity.IncarnationID {
			return sdkmodels.ReplicaRegistrationResponse{}, sdkmodels.ErrReplicaLeaseExpired
		}
	}
	if persist != nil {
		if err := persist(ctx, request); err != nil {
			return sdkmodels.ReplicaRegistrationResponse{}, err
		}
	}
	leaseExpires := now.Add(time.Duration(directory.contract.Lease.TTLSeconds) * time.Second)
	directory.entries[key] = LivePluginReplica{Registration: cloneRegistration(request), LeaseExpires: leaseExpires}
	directory.instances[request.Identity.InstanceID] = struct{}{}
	changed = true
	return sdkmodels.ReplicaRegistrationResponse{
		ContractVersion: directory.contract.ContractVersion,
		Identity:        request.Identity,
		ServerTime:      now,
		LeaseExpiresAt:  leaseExpires,
	}, nil
}

// Renew updates only readiness and the acknowledged config generation of the
// currently registered incarnation.
func (directory *PluginReplicaDirectory) Renew(request sdkmodels.ReplicaRenewalRequest, certificate *x509.Certificate) (sdkmodels.ReplicaLease, error) {
	if directory == nil || request.Validate() != nil || request.ContractVersion != directory.contract.ContractVersion {
		return sdkmodels.ReplicaLease{}, sdkmodels.ErrInvalidReplicaRegistration
	}
	if !directory.certificateMatches(certificate, request.Identity) {
		return sdkmodels.ReplicaLease{}, sdkmodels.ErrReplicaIdentityMismatch
	}
	now := directory.now().UTC()
	key := replicaDirectoryKey{instanceID: request.Identity.InstanceID, replicaID: request.Identity.ReplicaID}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	changed := false
	defer func() {
		if changed && directory.notify != nil {
			directory.notify()
		}
	}()
	entry, exists := directory.entries[key]
	if !exists {
		return sdkmodels.ReplicaLease{}, sdkmodels.ErrReplicaNotRegistered
	}
	if entry.Registration.Identity != request.Identity {
		return sdkmodels.ReplicaLease{}, sdkmodels.ErrReplicaIdentityConflict
	}
	if !entry.LeaseExpires.After(now) {
		return sdkmodels.ReplicaLease{}, sdkmodels.ErrReplicaLeaseExpired
	}
	entry.Registration.AppliedGeneration = request.AppliedGeneration
	entry.Registration.Ready = request.Ready
	entry.LeaseExpires = now.Add(time.Duration(directory.contract.Lease.TTLSeconds) * time.Second)
	directory.entries[key] = entry
	changed = true
	return sdkmodels.ReplicaLease{
		ContractVersion: directory.contract.ContractVersion,
		Identity:        request.Identity,
		ServerTime:      now,
		LeaseExpiresAt:  entry.LeaseExpires,
	}, nil
}

// Deregister removes exactly the authenticated incarnation from eligibility.
func (directory *PluginReplicaDirectory) Deregister(request sdkmodels.ReplicaDeregistrationRequest, certificate *x509.Certificate) (sdkmodels.ReplicaDeregistrationResponse, error) {
	if directory == nil || request.Validate() != nil || request.ContractVersion != directory.contract.ContractVersion {
		return sdkmodels.ReplicaDeregistrationResponse{}, sdkmodels.ErrInvalidReplicaRegistration
	}
	if !directory.certificateMatches(certificate, request.Identity) {
		return sdkmodels.ReplicaDeregistrationResponse{}, sdkmodels.ErrReplicaIdentityMismatch
	}
	key := replicaDirectoryKey{instanceID: request.Identity.InstanceID, replicaID: request.Identity.ReplicaID}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	changed := false
	defer func() {
		if changed && directory.notify != nil {
			directory.notify()
		}
	}()
	entry, exists := directory.entries[key]
	if !exists {
		return sdkmodels.ReplicaDeregistrationResponse{}, sdkmodels.ErrReplicaNotRegistered
	}
	if entry.Registration.Identity != request.Identity {
		return sdkmodels.ReplicaDeregistrationResponse{}, sdkmodels.ErrReplicaIdentityConflict
	}
	delete(directory.entries, key)
	changed = true
	return sdkmodels.ReplicaDeregistrationResponse{
		ContractVersion: directory.contract.ContractVersion,
		Identity:        request.Identity,
		Deregistered:    true,
	}, nil
}

// Resolve returns the registered logical instance for a verified certificate
// only while that exact replica lease remains active.
func (directory *PluginReplicaDirectory) Resolve(certificate *x509.Certificate) (string, bool) {
	if directory == nil || certificate == nil {
		return "", false
	}
	now := directory.now().UTC()
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	for _, entry := range directory.entries {
		if entry.LeaseExpires.After(now) && directory.certificateMatches(certificate, entry.Registration.Identity) {
			return entry.Registration.Identity.InstanceID, true
		}
	}
	return "", false
}

// ResolveIdentity returns the full authenticated replica identity of the exact
// live incarnation named by a verified certificate. Unlike Resolve it never
// falls back to a declared instance, because the peer directory is scoped to a
// concrete, registered replica identity.
func (directory *PluginReplicaDirectory) ResolveIdentity(certificate *x509.Certificate) (sdkmodels.PeerReplicaID, bool) {
	if directory == nil || certificate == nil {
		return sdkmodels.PeerReplicaID{}, false
	}
	now := directory.now().UTC()
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	for _, entry := range directory.entries {
		if entry.LeaseExpires.After(now) && directory.certificateMatches(certificate, entry.Registration.Identity) {
			return entry.Registration.Identity, true
		}
	}
	return sdkmodels.PeerReplicaID{}, false
}

// MarkRegisteredInstances restores only the durable source-of-truth markers.
// Replica endpoints, leases, readiness and identities still require a fresh
// authenticated registration after Core restart.
func (directory *PluginReplicaDirectory) MarkRegisteredInstances(instanceIDs []string) {
	if directory == nil || len(instanceIDs) == 0 {
		return
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	for _, instanceID := range instanceIDs {
		if instanceID != "" {
			directory.instances[instanceID] = struct{}{}
		}
	}
}

// Snapshot returns a stable copy of all unexpired replicas.
func (directory *PluginReplicaDirectory) Snapshot() []LivePluginReplica {
	if directory == nil {
		return nil
	}
	now := directory.now().UTC()
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	replicas := make([]LivePluginReplica, 0, len(directory.entries))
	for _, entry := range directory.entries {
		if entry.LeaseExpires.After(now) {
			replicas = append(replicas, LivePluginReplica{Registration: cloneRegistration(entry.Registration), LeaseExpires: entry.LeaseExpires})
		}
	}
	return replicas
}

// RegisteredInstanceIDs returns sorted instance IDs with at least one live
// authenticated incarnation. It is a snapshot for bounded recovery scans.
func (directory *PluginReplicaDirectory) RegisteredInstanceIDs() []string {
	if directory == nil {
		return nil
	}
	instances := make(map[string]struct{})
	for _, replica := range directory.Snapshot() {
		instanceID := replica.Registration.Identity.InstanceID
		if instanceID != "" {
			instances[instanceID] = struct{}{}
		}
	}
	result := make([]string, 0, len(instances))
	for instanceID := range instances {
		result = append(result, instanceID)
	}
	sort.Strings(result)
	return result
}

// HasRegisteredInstance reports whether an instance has a durable registration
// record. The marker survives lease expiry and deregistration so recovery can
// resume settings only after an authenticated replica returns.
func (directory *PluginReplicaDirectory) HasRegisteredInstance(instanceID string) bool {
	if directory == nil || instanceID == "" {
		return false
	}
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	_, exists := directory.instances[instanceID]
	return exists
}

func (directory *PluginReplicaDirectory) certificateMatches(certificate *x509.Certificate, identity sdkmodels.PeerReplicaID) bool {
	if certificate == nil {
		return false
	}
	expected, err := directory.contract.ReplicaIdentityURI(identity)
	if err != nil {
		return false
	}
	for _, identifier := range certificate.URIs {
		if identifier.String() == expected {
			return true
		}
	}
	return false
}

func sameImmutableRegistration(left, right sdkmodels.ReplicaRegistrationRequest) bool {
	return left.Identity == right.Identity && left.RestEndpoint == right.RestEndpoint &&
		samePeerEndpoints(left.PeerEndpoints, right.PeerEndpoints) && left.Release == right.Release &&
		sameContractVersions(left.AdvertisedContracts, right.AdvertisedContracts) &&
		sameContractRanges(left.AcceptedContracts, right.AcceptedContracts)
}

func samePeerEndpoints(left, right []sdkmodels.ReplicaPeerEndpoint) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameContractVersions(left, right []sdkmodels.ContractVersion) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameContractRanges(left, right []sdkmodels.ContractRange) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneRegistration(request sdkmodels.ReplicaRegistrationRequest) sdkmodels.ReplicaRegistrationRequest {
	request.PeerEndpoints = append([]sdkmodels.ReplicaPeerEndpoint(nil), request.PeerEndpoints...)
	request.AdvertisedContracts = append([]sdkmodels.ContractVersion(nil), request.AdvertisedContracts...)
	request.AcceptedContracts = append([]sdkmodels.ContractRange(nil), request.AcceptedContracts...)
	return request
}
