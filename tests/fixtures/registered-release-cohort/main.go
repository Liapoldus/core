package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/core/v3/internal/infrastructure/plugins"
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
)

type source struct {
	replicas []plugins.LivePluginReplica
}

func (directory source) Snapshot() []plugins.LivePluginReplica { return directory.replicas }
func (directory source) HasRegisteredInstance(instanceID string) bool {
	for _, replica := range directory.replicas {
		if replica.Registration.Identity.InstanceID == instanceID {
			return true
		}
	}
	return false
}

func main() {
	now := time.Date(2026, time.October, 6, 10, 0, 0, 0, time.UTC)
	sameDigest := [2]sdkmodels.ReplicaRegistrationRequest{
		registration("replica-a", "same-release"), registration("replica-b", "same-release"),
	}
	mutual := compatiblePair("1.0.0", "1.1.0", "2.0.0", "2.0.0")
	oneWay := compatiblePair("1.0.0", "1.1.0", "1.1.0", "2.0.0")
	noEvidence := [2]sdkmodels.ReplicaRegistrationRequest{
		registration("replica-a", "no-evidence-a"), registration("replica-b", "no-evidence-b"),
	}
	write(map[string]bool{
		"sameReleaseAllowed":                  capture(sameDigest[:], now),
		"mutuallyCompatibleReleasesAllowed":   capture(mutual[:], now),
		"oneWayCompatibilityBlocked":          !capture(oneWay[:], now),
		"missingCompatibilityEvidenceBlocked": !capture(noEvidence[:], now),
	})
}

func capture(registrations []sdkmodels.ReplicaRegistrationRequest, now time.Time) bool {
	replicas := make([]plugins.LivePluginReplica, 0, len(registrations))
	for _, registration := range registrations {
		replicas = append(replicas, plugins.LivePluginReplica{
			Registration: registration,
			LeaseExpires: now.Add(time.Minute),
		})
	}
	registered := plugins.NewRegisteredReplicaReloadResolver(source{replicas: replicas}, nil, func() time.Time { return now })
	applier := &plugins.SDKConfigurationApplier{Registered: registered}
	targets, found, err := applier.CaptureConfigurationTargets(context.Background(), "forms")
	return found && err == nil && len(targets) == len(registrations)
}

func registration(replicaID, release string) sdkmodels.ReplicaRegistrationRequest {
	return sdkmodels.ReplicaRegistrationRequest{
		ContractVersion: "liapoldus.plugin-sdk.replica-lifecycle.v2",
		Identity: sdkmodels.PeerReplicaID{
			InstanceID: "forms", ReplicaID: replicaID, IncarnationID: "inc-" + replicaID, PlacementID: "zone-a",
		},
		RestEndpoint:        "https://127.0.0.1:9443",
		PeerEndpoints:       []sdkmodels.ReplicaPeerEndpoint{},
		Release:             sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: digest(release)},
		AdvertisedContracts: []sdkmodels.ContractVersion{},
		AcceptedContracts:   []sdkmodels.ContractRange{},
	}
}

func compatiblePair(versionA, versionB, maxA, maxB string) [2]sdkmodels.ReplicaRegistrationRequest {
	left := registration("replica-a", "release-a")
	right := registration("replica-b", "release-b")
	left.AdvertisedContracts = []sdkmodels.ContractVersion{{ContractID: "org.example.settings-schema", Version: versionA, SHA256: digest("schema-" + versionA)}}
	right.AdvertisedContracts = []sdkmodels.ContractVersion{{ContractID: "org.example.settings-schema", Version: versionB, SHA256: digest("schema-" + versionB)}}
	left.AcceptedContracts = []sdkmodels.ContractRange{{ContractID: "org.example.settings-schema", MinimumVersion: "1.0.0", MaximumVersionExclusive: maxA}}
	right.AcceptedContracts = []sdkmodels.ContractRange{{ContractID: "org.example.settings-schema", MinimumVersion: "1.0.0", MaximumVersionExclusive: maxB}}
	return [2]sdkmodels.ReplicaRegistrationRequest{left, right}
}

func digest(value string) string {
	valueDigest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", valueDigest)
}

func write(result map[string]bool) {
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}
