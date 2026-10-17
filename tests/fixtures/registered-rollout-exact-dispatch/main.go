package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/services"
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
)

const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type source struct{ replicas []services.LiveServiceReplica }

func (value *source) Snapshot() []services.LiveServiceReplica {
	return append([]services.LiveServiceReplica(nil), value.replicas...)
}
func (*source) HasRegisteredInstance(string) bool { return true }

type configReader struct {
	interfaces.ServiceConfigurationStore
	revision models.ServiceConfigurationRevision
}

func (reader configReader) Current(context.Context, string) (models.ServiceConfigurationRevision, models.ServiceConfigurationPointers, error) {
	return reader.revision, models.ServiceConfigurationPointers{}, nil
}

type reloadClient struct {
	identity sdkmodels.PeerReplicaID
	calls    *[]string
}

func (client reloadClient) Reload(_ context.Context, reload sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	*client.calls = append(*client.calls, client.identity.ReplicaID+"/"+client.identity.IncarnationID)
	return sdkmodels.ReloadAcknowledgement{Generation: reload.Generation, SHA256: reload.SHA256,
		SchemaVersion: reload.SchemaVersion, Applied: true, Outcome: sdkmodels.OutcomeApplied}, nil
}

func main() {
	ctx := context.Background()
	raw := []byte(`{"config":"exact"}`)
	revision := models.ServiceConfigurationRevision{InstanceID: "instance-one", Revision: 7,
		SchemaVersion: 2, Digest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", SettingsJSON: raw}
	calls := []string{}
	current := &source{replicas: []services.LiveServiceReplica{
		live("replica-a", "incarnation-new"), live("replica-b", "incarnation-b"),
	}}
	resolver := services.NewRegisteredReplicaReloadResolver(current, func(_ context.Context, replica services.LiveServiceReplica) (services.SDKReloadClient, func(), error) {
		return reloadClient{identity: replica.Registration.Identity, calls: &calls}, nil, nil
	}, nil)
	applier := &services.SDKConfigurationApplier{Store: configReader{revision: revision}, Registered: resolver}
	targets := []models.ServiceRolloutTarget{
		{ReplicaID: "replica-a", IncarnationID: "incarnation-a", ReleaseSHA256: digest, LeaseExpiresAt: time.Now().UTC().Add(time.Minute)},
		{ReplicaID: "replica-b", IncarnationID: "incarnation-b", ReleaseSHA256: digest, LeaseExpiresAt: time.Now().UTC().Add(time.Minute)},
	}
	firstAck, firstErr := applier.ApplyConfigurationToTargets(ctx, "operation-one", revision.InstanceID, "7", raw, targets)
	firstPending := firstErr != nil
	firstCalls := append([]string(nil), calls...)

	calls = []string{}
	current.replicas = []services.LiveServiceReplica{live("replica-a", "incarnation-a"), live("replica-b", "incarnation-b")}
	targets[1].Acknowledged = true
	secondAck, secondErr := applier.ApplyConfigurationToTargets(ctx, "operation-one", revision.InstanceID, "7", raw, targets)
	check(secondErr)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"firstPending":           firstPending,
		"firstAck":               identities(firstAck),
		"firstCalls":             firstCalls,
		"replacementNeverCalled": !contains(firstCalls, "replica-a/incarnation-new"),
		"secondAck":              identities(secondAck),
		"secondCalls":            calls,
	}))
}

func live(replicaID, incarnationID string) services.LiveServiceReplica {
	identity := sdkmodels.PeerReplicaID{InstanceID: "instance-one", ReplicaID: replicaID,
		IncarnationID: incarnationID, PlacementID: "placement-one"}
	return services.LiveServiceReplica{Registration: sdkmodels.ReplicaRegistrationRequest{
		Identity: identity, Release: sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: digest},
	}, LeaseExpires: time.Now().UTC().Add(time.Minute)}
}

func identities(targets []models.ServiceRolloutTarget) []string {
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		result = append(result, target.ReplicaID+"/"+target.IncarnationID)
	}
	return result
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
