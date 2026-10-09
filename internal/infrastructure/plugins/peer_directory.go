package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
)

// PeerDirectory assembles the caller-scoped, Core-authorized view of peer links
// for one authenticated replica. It is built only from the explicit,
// deny-by-default link rules plus currently leased replicas: a target without a
// matching rule produces no link, and a replica without a usable endpoint for
// the rule carrier is recorded as not-ready rather than silently dropped.
//
// The returned generation is content-stable: it depends on the caller and the
// ordered link set, not on the assembly instant, so an unchanged directory keeps
// the same ETag across polls.
func (directory *PluginReplicaDirectory) PeerDirectory(
	caller sdkmodels.PeerReplicaID,
	rules []models.PeerLinkRule,
	now time.Time,
	ttl time.Duration,
) (sdkmodels.PeerDirectory, error) {
	if directory == nil || !caller.Valid() || ttl <= 0 || ttl > sdkmodels.PeerDirectoryMaximumTTL {
		return sdkmodels.PeerDirectory{}, ErrPeerDirectoryUnavailable
	}
	issuedAt := now.UTC()
	expiresAt := issuedAt.Add(ttl)
	leased := make(map[string][]LivePluginReplica)
	for _, replica := range directory.Snapshot() {
		instanceID := replica.Registration.Identity.InstanceID
		if instanceID == "" {
			continue
		}
		leased[instanceID] = append(leased[instanceID], replica)
	}

	links := make([]sdkmodels.PeerLink, 0)
	seen := make(map[string]struct{})
	for _, rule := range rules {
		if rule.CallerInstanceID != caller.InstanceID {
			continue
		}
		if err := rule.Validate(); err != nil {
			return sdkmodels.PeerDirectory{}, ErrPeerDirectoryUnavailable
		}
		linkID := "link." + rule.TargetInstanceID + "." + rule.Carrier
		if _, exists := seen[linkID]; exists {
			continue
		}
		seen[linkID] = struct{}{}
		links = append(links, sdkmodels.PeerLink{
			LinkID:                linkID,
			TargetInstanceID:      rule.TargetInstanceID,
			PlacementRule:         sdkmodels.PeerPlacementRule(rule.PlacementRule),
			Carrier:               sdkmodels.PeerCarrier(rule.Carrier),
			SecurityProfile:       sdkmodels.PeerSecurityMTLS,
			RequiredPeerContracts: peerDirectoryContracts(rule.RequiredContracts),
			Replicas:              peerDirectoryReplicas(leased[rule.TargetInstanceID], rule, caller, issuedAt, expiresAt),
		})
	}
	sort.Slice(links, func(left, right int) bool { return links[left].LinkID < links[right].LinkID })

	generation, err := peerDirectoryGeneration(caller, links)
	if err != nil {
		return sdkmodels.PeerDirectory{}, ErrPeerDirectoryUnavailable
	}
	return sdkmodels.PeerDirectory{
		ContractVersion: sdkmodels.PeerDirectoryContractVersion,
		Generation:      generation,
		IssuedAt:        issuedAt,
		ExpiresAt:       expiresAt,
		Caller:          caller,
		Links:           links,
	}, nil
}

func peerDirectoryContracts(requirements []models.PeerContractRange) []sdkmodels.ContractRange {
	contracts := make([]sdkmodels.ContractRange, 0, len(requirements))
	for _, requirement := range requirements {
		contracts = append(contracts, sdkmodels.ContractRange{
			ContractID:              requirement.ContractID,
			MinimumVersion:          requirement.MinimumVersion,
			MaximumVersionExclusive: requirement.MaximumVersionExclusive,
		})
	}
	return contracts
}

func peerDirectoryReplicas(
	live []LivePluginReplica,
	rule models.PeerLinkRule,
	caller sdkmodels.PeerReplicaID,
	issuedAt, expiresAt time.Time,
) []sdkmodels.PeerDirectoryReplica {
	replicas := make([]sdkmodels.PeerDirectoryReplica, 0, len(live))
	seen := make(map[string]struct{}, len(live))
	for _, replica := range live {
		identity := replica.Registration.Identity
		if !identity.Valid() || identity.InstanceID != rule.TargetInstanceID {
			continue
		}
		if !peerPlacementMatches(rule.PlacementRule, caller.PlacementID, identity.PlacementID) {
			continue
		}
		if _, exists := seen[identity.ReplicaID]; exists {
			continue
		}
		endpoint, usable := peerEndpointForCarrier(replica.Registration.PeerEndpoints, rule.Carrier)
		if !usable {
			continue
		}
		seen[identity.ReplicaID] = struct{}{}
		eligibility := sdkmodels.PeerEligibilityNotReady
		endpointValue := ""
		var eligibleUntil *time.Time
		if replica.Registration.Ready && replica.LeaseExpires.After(issuedAt) {
			eligibility = sdkmodels.PeerEligibilityReady
			endpointValue = endpoint
			lease := replica.LeaseExpires.UTC()
			if lease.After(expiresAt) {
				lease = expiresAt
			}
			eligibleUntil = &lease
		}
		peerContracts := make([]sdkmodels.ContractVersion, len(replica.Registration.AdvertisedContracts))
		copy(peerContracts, replica.Registration.AdvertisedContracts)
		replicas = append(replicas, sdkmodels.PeerDirectoryReplica{
			Identity:      identity,
			Endpoint:      endpointValue,
			Eligibility:   eligibility,
			EligibleUntil: eligibleUntil,
			Weight:        rule.Weight,
			PeerContracts: peerContracts,
		})
	}
	sort.Slice(replicas, func(left, right int) bool {
		return replicas[left].Identity.ReplicaID < replicas[right].Identity.ReplicaID
	})
	return replicas
}

func peerPlacementMatches(placement, callerPlacement, targetPlacement string) bool {
	switch placement {
	case models.PeerLinkPlacementSame:
		return callerPlacement == targetPlacement
	case models.PeerLinkPlacementRemote:
		return callerPlacement != targetPlacement
	default:
		return false
	}
}

func peerEndpointForCarrier(endpoints []sdkmodels.ReplicaPeerEndpoint, carrier string) (string, bool) {
	for _, endpoint := range endpoints {
		if string(endpoint.Carrier) == carrier && endpoint.Valid() {
			return endpoint.Endpoint, true
		}
	}
	return "", false
}

func peerDirectoryGeneration(caller sdkmodels.PeerReplicaID, links []sdkmodels.PeerLink) (string, error) {
	hashed := make([]sdkmodels.PeerLink, len(links))
	copy(hashed, links)
	for index := range hashed {
		replicas := make([]sdkmodels.PeerDirectoryReplica, len(hashed[index].Replicas))
		copy(replicas, hashed[index].Replicas)
		for replicaIndex := range replicas {
			replicas[replicaIndex].EligibleUntil = nil
		}
		hashed[index].Replicas = replicas
	}
	document, err := json.Marshal(struct {
		Caller sdkmodels.PeerReplicaID `json:"caller"`
		Links  []sdkmodels.PeerLink    `json:"links"`
	}{Caller: caller, Links: hashed})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:]), nil
}
