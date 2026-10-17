package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/services"
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
)

const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type source struct{ replicas []services.LiveServiceReplica }

func (value source) Snapshot() []services.LiveServiceReplica { return value.replicas }
func (source) HasRegisteredInstance(string) bool             { return true }

type store struct {
	active models.ServiceConfigurationRevision
}

func (value store) Current(context.Context, string) (models.ServiceConfigurationRevision, models.ServiceConfigurationPointers, error) {
	return value.active, models.ServiceConfigurationPointers{InstanceID: value.active.InstanceID, CurrentRevision: value.active.Revision}, nil
}
func (value store) GetRevision(context.Context, string, int64) (models.ServiceConfigurationRevision, error) {
	return value.active, nil
}
func (store) CreateCandidate(context.Context, string, string, string, string, int64, int64, []byte, models.AuditRecord) (models.ServiceConfigurationRevision, error) {
	return models.ServiceConfigurationRevision{}, errors.New("unused")
}
func (store) ActivateCandidate(context.Context, string, int64, int64, models.AuditRecord) (models.ServiceConfigurationPointers, error) {
	return models.ServiceConfigurationPointers{}, errors.New("unused")
}
func (store) FailCandidate(context.Context, string, int64, int64, models.AuditRecord) (models.ServiceConfigurationPointers, error) {
	return models.ServiceConfigurationPointers{}, errors.New("unused")
}
func (store) RestorePrevious(context.Context, string, int64, models.AuditRecord) (models.ServiceConfigurationPointers, error) {
	return models.ServiceConfigurationPointers{}, errors.New("unused")
}

type observation struct {
	mu     sync.Mutex
	values map[string]bool
}

func (recorder *observation) RecordReplicaObservation(_ context.Context, _, replicaID, _ string, _ int64, acknowledged, _ bool) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.values[replicaID] = acknowledged
}

type replicaClient struct {
	mu            sync.Mutex
	instanceID    string
	replicaID     string
	generation    string
	sha256        string
	schemaVersion string
	ready         bool
	reloads       int
	failFirst     bool
}

func (client *replicaClient) Readiness(context.Context) (sdkmodels.Readiness, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	return sdkmodels.Readiness{
		InstanceID: client.instanceID, ReplicaID: client.replicaID, Generation: client.generation,
		SHA256: client.sha256, SchemaVersion: client.schemaVersion, Ready: client.ready,
	}, nil
}

func (client *replicaClient) Reload(_ context.Context, request sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.reloads++
	if client.failFirst && client.reloads == 1 {
		return sdkmodels.ReloadAcknowledgement{}, services.ErrServiceUnavailable
	}
	client.generation, client.sha256, client.ready = request.Generation, request.SHA256, true
	return sdkmodels.ReloadAcknowledgement{
		Generation: request.Generation, SHA256: request.SHA256, SchemaVersion: request.SchemaVersion,
		Applied: true, Outcome: sdkmodels.OutcomeApplied,
	}, nil
}

func (client *replicaClient) reloadCount() int {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.reloads
}

func main() {
	now := time.Now().UTC()
	active := models.ServiceConfigurationRevision{
		InstanceID: "server", Revision: 7, SchemaVersion: 1, Digest: digest,
		SettingsJSON: []byte(`{"mode":"test"}`),
	}
	replicas := []services.LiveServiceReplica{
		live("replica-current", now, true, "7", digest),
		live("replica-stale", now, true, "6", digest),
		live("replica-flaky", now, false, "6", digest),
		live("replica-expired", now.Add(-time.Minute), false, "6", digest),
	}
	clients := map[string]*replicaClient{
		"replica-current": {instanceID: "server", replicaID: "replica-current", generation: "7", sha256: digest, schemaVersion: "1", ready: true},
		"replica-stale":   {instanceID: "server", replicaID: "replica-stale", generation: "6", sha256: digest, schemaVersion: "1", ready: true},
		"replica-flaky":   {instanceID: "server", replicaID: "replica-flaky", generation: "6", sha256: digest, schemaVersion: "1", failFirst: true},
		"replica-expired": {instanceID: "server", replicaID: "replica-expired", generation: "6", sha256: digest, schemaVersion: "1"},
	}
	resolver := services.NewRegisteredReplicaReloadResolver(source{replicas: replicas}, func(_ context.Context, replica services.LiveServiceReplica) (services.SDKReloadClient, func(), error) {
		client := clients[replica.Registration.Identity.ReplicaID]
		if client == nil {
			return nil, nil, services.ErrServiceUnavailable
		}
		return client, func() {}, nil
	}, func() time.Time { return now })
	recorder := &observation{values: map[string]bool{}}
	applier := &services.SDKConfigurationApplier{Store: store{active: active}, Registered: resolver, Observations: recorder}
	firstErr := applier.ReconcileRegisteredReplicas(context.Background())
	if firstErr == nil {
		panic("partial refusal was hidden")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = applier.RunRegisteredReconciliation(ctx, 5*time.Millisecond)
	}()
	deadline := time.Now().Add(time.Second)
	for clients["replica-flaky"].reloadCount() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done

	clients["replica-current"].mu.Lock()
	currentCount := clients["replica-current"].reloads
	clients["replica-current"].mu.Unlock()
	clients["replica-stale"].mu.Lock()
	staleCount := clients["replica-stale"].reloads
	clients["replica-stale"].mu.Unlock()
	clients["replica-expired"].mu.Lock()
	expiredCount := clients["replica-expired"].reloads
	clients["replica-expired"].mu.Unlock()
	recorder.mu.Lock()
	observationsMatch := recorder.values["replica-current"] && recorder.values["replica-stale"] && recorder.values["replica-flaky"]
	recorder.mu.Unlock()

	result := map[string]bool{
		"currentGenerationSkipped":           currentCount == 0,
		"staleGenerationReloadedExactlyOnce": staleCount == 1,
		"failedReloadRetried":                clients["replica-flaky"].reloadCount() >= 2,
		"expiredReplicaSkipped":              expiredCount == 0,
		"exactAcknowledgementsRecorded":      observationsMatch,
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}

func live(replicaID string, now time.Time, ready bool, generation, sha256 string) services.LiveServiceReplica {
	return services.LiveServiceReplica{
		Registration: sdkmodels.ReplicaRegistrationRequest{
			Identity:     sdkmodels.PeerReplicaID{InstanceID: "server", ReplicaID: replicaID, IncarnationID: "inc-1", PlacementID: "node-a"},
			RestEndpoint: "https://" + replicaID + ".example:9443", Ready: ready, AppliedGeneration: generation,
		},
		LeaseExpires: now.Add(time.Minute),
	}
}
