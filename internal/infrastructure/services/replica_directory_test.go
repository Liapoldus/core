package services

import (
	"crypto/x509"
	"net/url"
	"strings"
	"testing"
	"time"

	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
)

func TestReplicaDirectoryRejectsCertificateMatchingMultipleLiveReplicas(t *testing.T) {
	contract, directory := newReplicaDirectoryTestFixture(t)
	first := sdkmodels.PeerReplicaID{
		InstanceID: "server", ReplicaID: "replica-1", IncarnationID: "incarnation-1", PlacementID: "local",
	}
	second := sdkmodels.PeerReplicaID{
		InstanceID: "server", ReplicaID: "replica-2", IncarnationID: "incarnation-2", PlacementID: "local",
	}
	for _, identity := range []sdkmodels.PeerReplicaID{first, second} {
		certificate := replicaDirectoryTestCertificate(t, contract, []sdkmodels.PeerReplicaID{identity}, identity.ReplicaID)
		if _, err := directory.Register(replicaDirectoryTestRegistration(contract, identity), certificate); err != nil {
			t.Fatalf("register replica %q: %v", identity.ReplicaID, err)
		}
	}

	ambiguous := replicaDirectoryTestCertificate(t, contract, []sdkmodels.PeerReplicaID{first, second}, "ambiguous-leaf")
	if _, ok := directory.ResolveIdentity(ambiguous); ok {
		t.Fatal("certificate matching more than one live replica identity was resolved")
	}
	if _, ok := directory.Resolve(ambiguous); ok {
		t.Fatal("ambiguous certificate was resolved through the instance-only API")
	}
}

func newReplicaDirectoryTestFixture(t *testing.T) (SDKReplicaLifecycleContract, *ServiceReplicaDirectory) {
	t.Helper()
	contract, err := LoadSDKReplicaLifecycleContract()
	if err != nil {
		t.Fatalf("load SDK replica lifecycle contract: %v", err)
	}
	now := time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)
	directory, err := NewServiceReplicaDirectory(contract, func() time.Time { return now })
	if err != nil {
		t.Fatalf("create replica directory: %v", err)
	}
	return contract, directory
}

func replicaDirectoryTestRegistration(contract SDKReplicaLifecycleContract, identity sdkmodels.PeerReplicaID) sdkmodels.ReplicaRegistrationRequest {
	return sdkmodels.ReplicaRegistrationRequest{
		ContractVersion:     contract.ContractVersion,
		Identity:            identity,
		RestEndpoint:        "https://server.example:9443",
		PeerEndpoints:       []sdkmodels.ReplicaPeerEndpoint{},
		Release:             sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: strings.Repeat("a", 64)},
		AdvertisedContracts: []sdkmodels.ContractVersion{},
		AcceptedContracts:   []sdkmodels.ContractRange{},
		AppliedGeneration:   "generation-1",
		Ready:               true,
	}
}

func replicaDirectoryTestCertificate(t *testing.T, contract SDKReplicaLifecycleContract, identities []sdkmodels.PeerReplicaID, raw string) *x509.Certificate {
	t.Helper()
	certificate := &x509.Certificate{Raw: []byte(raw)}
	for _, identity := range identities {
		value, err := contract.ReplicaIdentityURI(identity)
		if err != nil {
			t.Fatalf("build replica identity URI: %v", err)
		}
		parsed, err := url.Parse(value)
		if err != nil {
			t.Fatalf("parse replica identity URI: %v", err)
		}
		certificate.URIs = append(certificate.URIs, parsed)
	}
	return certificate
}
