package plugins

import (
	"context"
	"testing"
	"time"

	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
)

type inProcessReloadClient struct{}

func (inProcessReloadClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}

type inProcessRolloutClient struct{}

func (inProcessRolloutClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}

func (inProcessRolloutClient) Readiness(context.Context) (sdkmodels.Readiness, error) {
	return sdkmodels.Readiness{Ready: true, InstanceID: "server", ReplicaID: "replica-1"}, nil
}

func (inProcessRolloutClient) ValidateConfiguration(context.Context, []byte) error { return nil }

func TestInProcessResolutionIsExplicitAndDoesNotFallback(t *testing.T) {
	client := inProcessReloadClient{}
	applier := &SDKConfigurationApplier{InProcess: map[string][]SDKReloadReplicaClient{
		"server": {{ReplicaID: "replica-1", Client: client}},
	}}
	resolved, release, found, err := applier.resolve(context.Background(), "server")
	if err != nil || !found || release != nil || resolved == nil {
		t.Fatalf("in-process resolution = %v, release=%t, found=%v, err=%v", resolved, release != nil, found, err)
	}
	if _, _, found, err := applier.resolve(context.Background(), "forms-db"); found || err == nil {
		t.Fatalf("missing in-process instance must fail closed: found=%v err=%v", found, err)
	}
}

func TestInProcessRolloutUsesImmutableCohortAndPluginValidation(t *testing.T) {
	client := inProcessRolloutClient{}
	registration := sdkmodels.ReplicaRegistrationRequest{
		ContractVersion:     "liapoldus.plugin-sdk.replica-lifecycle.v2",
		Identity:            sdkmodels.PeerReplicaID{InstanceID: "server", ReplicaID: "replica-1", IncarnationID: "inc-1", PlacementID: "in-process"},
		RestEndpoint:        "https://in-process.invalid",
		PeerEndpoints:       []sdkmodels.ReplicaPeerEndpoint{},
		Release:             sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		AdvertisedContracts: []sdkmodels.ContractVersion{}, AcceptedContracts: []sdkmodels.ContractRange{},
	}
	applier := &SDKConfigurationApplier{
		InProcessRegistrations: map[string][]InProcessReplicaRegistration{
			"server": {{Registration: registration, Client: SDKReloadReplicaClient{ReplicaID: "replica-1", Client: client}}},
		},
	}
	targets, found, err := applier.CaptureConfigurationTargets(context.Background(), "server")
	if err != nil || !found || len(targets) != 1 || !targets[0].Valid() {
		t.Fatalf("capture = %#v found=%v err=%v", targets, found, err)
	}
	if err := applier.ValidateTrafficRolloutConfiguration(context.Background(), "server", []byte(`{"enabled":true}`)); err != nil {
		t.Fatalf("in-process validation: %v", err)
	}
	replicas, err := applier.TrafficRolloutReplicas(context.Background(), "server")
	if err != nil || len(replicas) != 1 || !replicas[0].Ready || !replicas[0].LeaseExpiresAt.After(time.Now()) {
		t.Fatalf("replicas = %#v err=%v", replicas, err)
	}
	registration.Identity.IncarnationID = "inc-2"
	applier.InProcessRegistrations["server"][0].Registration = registration
	lost, err := applier.LostConfigurationTargets(context.Background(), "server", targets)
	if err != nil || len(lost) != 1 {
		t.Fatalf("lost = %#v err=%v", lost, err)
	}
}
