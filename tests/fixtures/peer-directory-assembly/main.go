package main

import (
	"crypto/x509"
	"encoding/json"
	"net/url"
	"os"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/plugins"
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/v2/infrastructure"
)

func main() {
	contract, err := sdkinfrastructure.LoadReplicaLifecycleContract()
	check(err)
	now := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	directory, err := plugins.NewPluginReplicaDirectory(contract, func() time.Time { return now })
	check(err)

	caller := replicaIdentity("caller", "replica-1", "inc-1", "node-b")
	formsContract := sdkmodels.ContractVersion{
		ContractID: "liapoldus.formsdb.v1",
		Version:    "1.0.0",
		SHA256:     "1111111111111111111111111111111111111111111111111111111111111111",
	}

	registerReplica(directory, contract, replicaIdentity("server", "replica-a", "inc-a", "node-a"),
		true, tcpEndpoint("server-a.internal:9443"), []sdkmodels.ContractVersion{formsContract})
	registerReplica(directory, contract, replicaIdentity("server", "replica-b", "inc-b", "node-a"),
		false, tcpEndpoint("server-b.internal:9443"), []sdkmodels.ContractVersion{formsContract})
	registerReplica(directory, contract, replicaIdentity("local", "replica-c", "inc-c", "node-b"),
		true, unixEndpoint("/run/liapoldus/local.sock"), []sdkmodels.ContractVersion{formsContract})
	registerReplica(directory, contract, replicaIdentity("hidden", "replica-d", "inc-d", "node-x"),
		true, tcpEndpoint("hidden.internal:9443"), []sdkmodels.ContractVersion{formsContract})

	rules := []models.PeerLinkRule{
		{
			CallerInstanceID: "caller",
			TargetInstanceID: "server",
			PlacementRule:    models.PeerLinkPlacementRemote,
			Carrier:          models.PeerLinkCarrierTCP,
			Weight:           50,
			RequiredContracts: []models.PeerContractRange{
				{ContractID: "liapoldus.formsdb.v1", MinimumVersion: "1.0.0", MaximumVersionExclusive: "2.0.0"},
			},
		},
		{
			CallerInstanceID: "caller",
			TargetInstanceID: "local",
			PlacementRule:    models.PeerLinkPlacementSame,
			Carrier:          models.PeerLinkCarrierUnix,
			Weight:           10,
		},
	}

	assembly, err := directory.PeerDirectory(caller, rules, now, 20*time.Second)
	check(err)
	raw, err := json.Marshal(assembly)
	check(err)
	parsed, parseErr := sdkmodels.ParsePeerDirectory(raw)

	serverLink, serverFound := linkFor(parsed, "server")
	_, localFound := linkFor(parsed, "local")
	_, hiddenFound := linkFor(parsed, "hidden")
	readyHasEndpoint, notReadyWithoutEndpoint, notReadyNotEligible, weightPreserved := replicaStates(serverLink)

	empty, err := directory.PeerDirectory(caller, nil, now, 20*time.Second)
	check(err)
	second, err := directory.PeerDirectory(caller, rules, now, 20*time.Second)
	check(err)
	laterAssembly, err := directory.PeerDirectory(caller, rules, now.Add(7*time.Second), 20*time.Second)
	check(err)
	otherCaller := replicaIdentity("other", "replica-2", "inc-2", "node-b")
	scoped, err := directory.PeerDirectory(otherCaller, rules, now, 20*time.Second)
	check(err)
	_, overTTLErr := directory.PeerDirectory(caller, rules, now, 31*time.Second)

	result := map[string]bool{
		"parsedValid":                  parseErr == nil,
		"linkCount":                    len(parsed.Links) == 2,
		"remoteLinkPresent":            serverFound,
		"samePlacementLinkPresent":     localFound,
		"hiddenTargetDenied":           !hiddenFound,
		"readyReplicaHasEndpoint":      readyHasEndpoint,
		"notReadyReplicaHasNoEndpoint": notReadyWithoutEndpoint,
		"notReadyReplicaNotEligible":   notReadyNotEligible,
		"weightPreserved":              weightPreserved,
		"requiredContractPreserved": len(serverLink.RequiredPeerContracts) == 1 &&
			serverLink.RequiredPeerContracts[0].ContractID == "liapoldus.formsdb.v1",
		"emptyPolicyDeniesAll":   len(empty.Links) == 0,
		"generationStable":       assembly.Generation == second.Generation,
		"generationIgnoresTime":  assembly.Generation == laterAssembly.Generation,
		"callerScoped":           len(scoped.Links) == 0,
		"overMaximumTTLRejected": overTTLErr == plugins.ErrPeerDirectoryUnavailable,
	}
	check(json.NewEncoder(os.Stdout).Encode(result))
}

func replicaIdentity(instanceID, replicaID, incarnationID, placementID string) sdkmodels.PeerReplicaID {
	return sdkmodels.PeerReplicaID{
		InstanceID:    instanceID,
		ReplicaID:     replicaID,
		IncarnationID: incarnationID,
		PlacementID:   placementID,
	}
}

func tcpEndpoint(value string) sdkmodels.ReplicaPeerEndpoint {
	return sdkmodels.ReplicaPeerEndpoint{Carrier: sdkmodels.PeerCarrierTCP, Endpoint: value, SecurityProfile: sdkmodels.PeerSecurityMTLS}
}

func unixEndpoint(value string) sdkmodels.ReplicaPeerEndpoint {
	return sdkmodels.ReplicaPeerEndpoint{Carrier: sdkmodels.PeerCarrierUnix, Endpoint: value, SecurityProfile: sdkmodels.PeerSecurityMTLS}
}

func registerReplica(directory *plugins.PluginReplicaDirectory, contract sdkinfrastructure.ReplicaLifecycleContract, identity sdkmodels.PeerReplicaID, ready bool, endpoint sdkmodels.ReplicaPeerEndpoint, advertised []sdkmodels.ContractVersion) {
	registration := sdkmodels.ReplicaRegistrationRequest{
		ContractVersion: contract.ContractVersion,
		Identity:        identity,
		RestEndpoint:    "https://forms-a.internal:9443",
		PeerEndpoints:   []sdkmodels.ReplicaPeerEndpoint{endpoint},
		Release: sdkmodels.ReplicaRelease{
			Version: "1.0.0",
			SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		},
		AdvertisedContracts: advertised,
		AcceptedContracts:   []sdkmodels.ContractRange{},
		AppliedGeneration:   "1",
		Ready:               ready,
	}
	_, err := directory.Register(registration, certificateFor(takeURI(contract, identity)))
	check(err)
}

func linkFor(directory sdkmodels.PeerDirectory, targetInstanceID string) (sdkmodels.PeerLink, bool) {
	for _, link := range directory.Links {
		if link.TargetInstanceID == targetInstanceID {
			return link, true
		}
	}
	return sdkmodels.PeerLink{}, false
}

func replicaStates(link sdkmodels.PeerLink) (ready bool, notReadyWithoutEndpoint bool, notReadyNotEligible bool, weightPreserved bool) {
	for _, item := range link.Replicas {
		switch item.Identity.ReplicaID {
		case "replica-a":
			ready = item.Eligibility == sdkmodels.PeerEligibilityReady && item.Endpoint != "" && item.EligibleUntil != nil
			weightPreserved = item.Weight == 50
		case "replica-b":
			notReadyWithoutEndpoint = item.Endpoint == "" && item.EligibleUntil == nil
			notReadyNotEligible = item.Eligibility == sdkmodels.PeerEligibilityNotReady
		}
	}
	return ready, notReadyWithoutEndpoint, notReadyNotEligible, weightPreserved
}

func takeURI(contract sdkinfrastructure.ReplicaLifecycleContract, identity sdkmodels.PeerReplicaID) string {
	uri, err := contract.ReplicaIdentityURI(identity)
	check(err)
	return uri
}

func certificateFor(value string) *x509.Certificate {
	identifier, err := url.Parse(value)
	check(err)
	return &x509.Certificate{URIs: []*url.URL{identifier}}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
