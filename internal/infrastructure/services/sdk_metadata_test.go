package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
)

type metadataTestClient struct{ replicaID string }

func (client metadataTestClient) resolvedReplicaID() string {
	if client.replicaID == "" {
		return "replica-1"
	}
	return client.replicaID
}

func (metadataTestClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}
func (metadataTestClient) Manifest(context.Context) ([]byte, error) {
	return []byte(`{"id":"server"}`), nil
}
func (metadataTestClient) ConfigurationSchema(context.Context) ([]byte, error) {
	return []byte(`{"type":"object"}`), nil
}
func (client metadataTestClient) Identity(context.Context) (sdkmodels.Registration, error) {
	return sdkmodels.Registration{InstanceID: "server", ReplicaID: client.resolvedReplicaID()}, nil
}
func (client metadataTestClient) Readiness(context.Context) (sdkmodels.Readiness, error) {
	return sdkmodels.Readiness{Ready: true, InstanceID: "server", ReplicaID: client.resolvedReplicaID()}, nil
}

type metadataTestSource struct{ replica LiveServiceReplica }

func (source metadataTestSource) Snapshot() []LiveServiceReplica {
	return []LiveServiceReplica{source.replica}
}
func (source metadataTestSource) HasRegisteredInstance(string) bool { return true }

type metadataTestReplicasSource []LiveServiceReplica

func (source metadataTestReplicasSource) Snapshot() []LiveServiceReplica { return source }
func (metadataTestReplicasSource) HasRegisteredInstance(string) bool     { return true }

func metadataTestRegistration() sdkmodels.ReplicaRegistrationRequest {
	return sdkmodels.ReplicaRegistrationRequest{
		Identity: sdkmodels.PeerReplicaID{InstanceID: "server", ReplicaID: "replica-1"},
		Release:  sdkmodels.ReplicaRelease{Version: "1.2.3", SHA256: strings.Repeat("a", 64)},
	}
}

func TestCoreMetadataResolutionUsesSDKClientOnly(t *testing.T) {
	registration := metadataTestRegistration()
	resolver := NewRegisteredReplicaReloadResolver(metadataTestSource{replica: LiveServiceReplica{Registration: registration, LeaseExpires: time.Now().Add(time.Minute)}}, func(context.Context, LiveServiceReplica) (SDKReloadClient, func(), error) {
		return metadataTestClient{}, nil, nil
	}, time.Now)
	metadata, err := resolver.Metadata(context.Background(), "server")
	if err != nil || string(metadata.Manifest) != `{"id":"server"}` || !metadata.Readiness.Ready ||
		metadata.ReplicaID != "replica-1" || metadata.Release.Version != "1.2.3" || metadata.Release.SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("metadata=%+v err=%v", metadata, err)
	}
}

func TestCoreMetadataIncludesEveryLiveReplicaRelease(t *testing.T) {
	first := metadataTestRegistration()
	second := metadataTestRegistration()
	second.Identity.ReplicaID = "replica-2"
	resolver := NewRegisteredReplicaReloadResolver(metadataTestReplicasSource{
		{Registration: second, LeaseExpires: time.Now().Add(time.Minute)},
		{Registration: first, LeaseExpires: time.Now().Add(time.Minute)},
	}, func(_ context.Context, replica LiveServiceReplica) (SDKReloadClient, func(), error) {
		return metadataTestClient{replicaID: replica.Registration.Identity.ReplicaID}, nil, nil
	}, time.Now)
	metadata, err := resolver.Metadata(context.Background(), "server")
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Replicas) != 2 || metadata.Replicas[0].ReplicaID != "replica-1" || metadata.Replicas[1].ReplicaID != "replica-2" {
		t.Fatalf("metadata did not return a deterministic release snapshot: %+v", metadata.Replicas)
	}
}

func TestCoreMetadataRejectsMixedLiveReplicaReleases(t *testing.T) {
	first := metadataTestRegistration()
	second := metadataTestRegistration()
	second.Identity.ReplicaID = "replica-2"
	second.Release.SHA256 = strings.Repeat("b", 64)
	resolver := NewRegisteredReplicaReloadResolver(metadataTestReplicasSource{
		{Registration: first, LeaseExpires: time.Now().Add(time.Minute)},
		{Registration: second, LeaseExpires: time.Now().Add(time.Minute)},
	}, func(context.Context, LiveServiceReplica) (SDKReloadClient, func(), error) {
		return metadataTestClient{}, nil, nil
	}, time.Now)
	if _, err := resolver.Metadata(context.Background(), "server"); !errors.Is(err, models.ErrServiceRegistrationMismatch) {
		t.Fatalf("mixed live release metadata error = %v", err)
	}
}

type metadataFailureTestClient struct{ err error }

func (metadataFailureTestClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}
func (client metadataFailureTestClient) Manifest(context.Context) ([]byte, error) {
	return nil, client.err
}
func (metadataFailureTestClient) ConfigurationSchema(context.Context) ([]byte, error) {
	return []byte(`{"type":"object"}`), nil
}
func (metadataFailureTestClient) Identity(context.Context) (sdkmodels.Registration, error) {
	return sdkmodels.Registration{InstanceID: "server", ReplicaID: "replica-1"}, nil
}
func (metadataFailureTestClient) Readiness(context.Context) (sdkmodels.Readiness, error) {
	return sdkmodels.Readiness{Ready: true, InstanceID: "server", ReplicaID: "replica-1"}, nil
}

func TestCoreMetadataFailureReportsOnlyServiceAndSDKPhase(t *testing.T) {
	registration := metadataTestRegistration()
	var gotInstance, gotPhase, gotError string
	resolver := NewRegisteredReplicaReloadResolver(metadataTestSource{replica: LiveServiceReplica{Registration: registration, LeaseExpires: time.Now().Add(time.Minute)}}, func(context.Context, LiveServiceReplica) (SDKReloadClient, func(), error) {
		return metadataFailureTestClient{err: errors.New("Service SDK control-plane reload call refused: deadline_exceeded")}, nil, nil
	}, time.Now)
	resolver.OnMetadataFailure = func(instanceID, phase string, err error) {
		gotInstance, gotPhase, gotError = instanceID, phase, err.Error()
	}

	if _, err := resolver.Metadata(context.Background(), "server"); err == nil {
		t.Fatal("expected live SDK metadata failure")
	}
	if gotInstance != "server" || gotPhase != "manifest" || gotError != "Service SDK control-plane reload call refused: deadline_exceeded" {
		t.Fatalf("metadata failure diagnostic = %q/%q/%q", gotInstance, gotPhase, gotError)
	}
}

type parallelMetadataTestClient struct {
	started chan<- string
	release <-chan struct{}
}

func (client parallelMetadataTestClient) wait(ctx context.Context, kind string) error {
	client.started <- kind
	select {
	case <-client.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (client parallelMetadataTestClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}
func (client parallelMetadataTestClient) Manifest(ctx context.Context) ([]byte, error) {
	return []byte(`{"id":"server"}`), client.wait(ctx, "manifest")
}
func (client parallelMetadataTestClient) ConfigurationSchema(ctx context.Context) ([]byte, error) {
	return []byte(`{"type":"object"}`), client.wait(ctx, "schema")
}
func (client parallelMetadataTestClient) Identity(ctx context.Context) (sdkmodels.Registration, error) {
	return sdkmodels.Registration{InstanceID: "server", ReplicaID: "replica-1"}, client.wait(ctx, "identity")
}
func (client parallelMetadataTestClient) Readiness(ctx context.Context) (sdkmodels.Readiness, error) {
	return sdkmodels.Readiness{Ready: true, InstanceID: "server", ReplicaID: "replica-1"}, client.wait(ctx, "readiness")
}

func TestCoreMetadataReadsServiceSDKDocumentsConcurrently(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	registration := metadataTestRegistration()
	resolver := NewRegisteredReplicaReloadResolver(metadataTestSource{replica: LiveServiceReplica{Registration: registration, LeaseExpires: time.Now().Add(time.Minute)}}, func(context.Context, LiveServiceReplica) (SDKReloadClient, func(), error) {
		return parallelMetadataTestClient{started: started, release: release}, nil, nil
	}, time.Now)

	result := make(chan error, 1)
	go func() {
		metadata, err := resolver.Metadata(context.Background(), "server")
		if err == nil && (string(metadata.Manifest) != `{"id":"server"}` || string(metadata.ConfigurationSchema) != `{"type":"object"}` || !metadata.Readiness.Ready) {
			err = errors.New("Core returned incomplete live Service SDK metadata")
		}
		result <- err
	}()

	seen := make(map[string]bool, 4)
	deadline := time.After(time.Second)
	for len(seen) < 4 {
		select {
		case kind := <-started:
			seen[kind] = true
		case <-deadline:
			t.Fatalf("metadata calls did not all start concurrently; started=%v", seen)
		}
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("metadata resolution did not finish after SDK responses were released")
	}
}

func TestRuntimeRegistrationMatchesAuthenticatedSDKArtifactAndEndpoints(t *testing.T) {
	now := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	digest := strings.Repeat("a", 64)
	live := LiveServiceReplica{
		Registration: sdkmodels.ReplicaRegistrationRequest{
			Identity:     sdkmodels.PeerReplicaID{InstanceID: "server", ReplicaID: "replica-1"},
			RestEndpoint: "https://server:9443",
			PeerEndpoints: []sdkmodels.ReplicaPeerEndpoint{{
				Carrier: sdkmodels.PeerCarrierTCP, Endpoint: "server:9444", SecurityProfile: sdkmodels.PeerSecurityMTLS,
			}},
			Release: sdkmodels.ReplicaRelease{Version: "1.2.3", SHA256: digest},
		},
		LeaseExpires: now.Add(time.Minute),
	}
	resolver := NewRegisteredReplicaReloadResolver(metadataTestSource{replica: live}, nil, func() time.Time { return now })
	claim := models.ServiceRegistration{
		ArtifactID: "server", ArtifactVersion: "1.2.3", Digest: "sha256:" + digest,
		InstanceID: "server", ReplicaID: "replica-1", ControlEndpoint: "https://server:9443",
		PeerEndpoint: "https://server:9444", RuntimeStatus: "started",
	}
	if err := resolver.ValidateRuntimeRegistration(context.Background(), claim); err != nil {
		t.Fatalf("matching Core and SDK claims rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*models.ServiceRegistration)
	}{
		{name: "artifact id", mutate: func(value *models.ServiceRegistration) { value.ArtifactID = "other" }},
		{name: "artifact version", mutate: func(value *models.ServiceRegistration) { value.ArtifactVersion = "1.2.4" }},
		{name: "artifact digest", mutate: func(value *models.ServiceRegistration) { value.Digest = "sha256:" + strings.Repeat("b", 64) }},
		{name: "control endpoint", mutate: func(value *models.ServiceRegistration) { value.ControlEndpoint = "https://other:9443" }},
		{name: "peer endpoint", mutate: func(value *models.ServiceRegistration) { value.PeerEndpoint = "https://other:9444" }},
		{name: "replica id", mutate: func(value *models.ServiceRegistration) { value.ReplicaID = "replica-2" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := claim
			test.mutate(&changed)
			err := resolver.ValidateRuntimeRegistration(context.Background(), changed)
			if !errors.Is(err, models.ErrServiceRegistrationMismatch) {
				t.Fatalf("mismatched claim error = %v", err)
			}
		})
	}

	expired := live
	expired.LeaseExpires = now
	expiredResolver := NewRegisteredReplicaReloadResolver(metadataTestSource{replica: expired}, nil, func() time.Time { return now })
	if err := expiredResolver.ValidateRuntimeRegistration(context.Background(), claim); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("expired replica error = %v", err)
	}
}
