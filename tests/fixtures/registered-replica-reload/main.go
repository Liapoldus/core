package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
)

type source struct {
	replicas []plugins.LivePluginReplica
	known    bool
}

func (value source) Snapshot() []plugins.LivePluginReplica { return value.replicas }
func (value source) HasRegisteredInstance(string) bool     { return value.known }

type recordingClient struct {
	id   string
	mu   *sync.Mutex
	seen *[]string
}

func (client recordingClient) Reload(_ context.Context, request sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	client.mu.Lock()
	*client.seen = append(*client.seen, client.id)
	client.mu.Unlock()
	return sdkmodels.ReloadAcknowledgement{
		Generation: request.Generation, SHA256: request.SHA256, SchemaVersion: request.SchemaVersion,
		Applied: true, Outcome: sdkmodels.OutcomeApplied,
	}, nil
}

func main() {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	registrations := []plugins.LivePluginReplica{
		{Registration: notReadyRegistration("replica-b", "https://replica-b.example:9443"), LeaseExpires: now.Add(time.Minute)},
		{Registration: registration("replica-expired", "https://expired.example:9443"), LeaseExpires: now.Add(-time.Second)},
		{Registration: registration("replica-a", "https://replica-a.example:9443"), LeaseExpires: now.Add(time.Minute)},
	}
	var mu sync.Mutex
	called := make([]string, 0, 2)
	released := 0
	resolver := plugins.NewRegisteredReplicaReloadResolver(source{replicas: registrations}, func(_ context.Context, replica plugins.LivePluginReplica) (plugins.SDKReloadClient, func(), error) {
		if replica.Registration.RestEndpoint == "https://bad.example:9443" {
			return nil, nil, plugins.ErrPluginUnavailable
		}
		return recordingClient{id: replica.Registration.Identity.ReplicaID, mu: &mu, seen: &called}, func() { released++ }, nil
	}, func() time.Time { return now })
	client, closeClients, found, err := resolver.Resolve(context.Background(), "forms")
	if err != nil || !found {
		panic("registered replicas did not resolve")
	}
	fanout, ok := client.(*plugins.SDKReloadFanout)
	if !ok {
		panic("registered replicas did not produce a fanout")
	}
	replicaIDs := make([]string, 0, len(fanout.Replicas))
	for _, replica := range fanout.Replicas {
		replicaIDs = append(replicaIDs, replica.ReplicaID)
	}
	_, results, callErr := fanout.ReloadObserved(context.Background(), sdkmodels.Reload{
		Generation: "2", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SchemaVersion: "1",
	})
	closeClients()
	if callErr != nil || len(results) != 2 {
		panic("registered replica fanout failed")
	}
	sort.Strings(called)

	emptyResolver := plugins.NewRegisteredReplicaReloadResolver(source{}, func(context.Context, plugins.LivePluginReplica) (plugins.SDKReloadClient, func(), error) {
		return nil, nil, errors.New("must not build")
	}, func() time.Time { return now })
	_, _, emptyFound, emptyErr := emptyResolver.Resolve(context.Background(), "forms")
	expiredResolver := plugins.NewRegisteredReplicaReloadResolver(source{replicas: registrations, known: true}, func(context.Context, plugins.LivePluginReplica) (plugins.SDKReloadClient, func(), error) {
		return nil, nil, errors.New("must not build")
	}, func() time.Time { return now.Add(2 * time.Minute) })
	_, _, expiredFound, expiredErr := expiredResolver.Resolve(context.Background(), "forms")

	partialClosed := 0
	badResolver := plugins.NewRegisteredReplicaReloadResolver(source{replicas: []plugins.LivePluginReplica{
		{Registration: registration("replica-good", "https://good.example:9443"), LeaseExpires: now.Add(time.Minute)},
		{Registration: registration("replica-zbad", "https://bad.example:9443"), LeaseExpires: now.Add(time.Minute)},
	}}, func(_ context.Context, replica plugins.LivePluginReplica) (plugins.SDKReloadClient, func(), error) {
		if replica.Registration.RestEndpoint == "https://bad.example:9443" {
			return nil, nil, plugins.ErrPluginUnavailable
		}
		return recordingClient{id: replica.Registration.Identity.ReplicaID, mu: &mu, seen: &called}, func() { partialClosed++ }, nil
	}, func() time.Time { return now })
	_, _, badFound, badErr := badResolver.Resolve(context.Background(), "forms")

	result := map[string]any{
		"found": found, "replicaIDs": replicaIDs, "called": called, "released": released,
		"emptyDirectoryUsesStatic":           emptyFound || emptyErr != nil,
		"expiredInstanceFenced":              expiredFound && errors.Is(expiredErr, plugins.ErrPluginUnavailable),
		"invalidMemberRejectedWholeSnapshot": badFound && errors.Is(badErr, plugins.ErrPluginUnavailable),
		"closedPartialClients":               partialClosed,
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}

func registration(replicaID, endpoint string) sdkmodels.ReplicaRegistrationRequest {
	return sdkmodels.ReplicaRegistrationRequest{
		Identity:     sdkmodels.PeerReplicaID{InstanceID: "forms", ReplicaID: replicaID, IncarnationID: "inc-1", PlacementID: "node-a"},
		RestEndpoint: endpoint,
		Ready:        true,
	}
}

func notReadyRegistration(replicaID, endpoint string) sdkmodels.ReplicaRegistrationRequest {
	request := registration(replicaID, endpoint)
	request.Ready = false
	return request
}
