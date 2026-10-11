package plugins

import (
	"context"
	"testing"
	"time"

	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
)

type metadataTestClient struct{}

func (metadataTestClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}
func (metadataTestClient) Manifest(context.Context) ([]byte, error) {
	return []byte(`{"id":"server"}`), nil
}
func (metadataTestClient) ConfigurationSchema(context.Context) ([]byte, error) {
	return []byte(`{"type":"object"}`), nil
}
func (metadataTestClient) Identity(context.Context) (sdkmodels.Registration, error) {
	return sdkmodels.Registration{InstanceID: "server", ReplicaID: "replica-1"}, nil
}
func (metadataTestClient) Readiness(context.Context) (sdkmodels.Readiness, error) {
	return sdkmodels.Readiness{Ready: true, InstanceID: "server", ReplicaID: "replica-1"}, nil
}

type metadataTestSource struct{ replica LivePluginReplica }

func (source metadataTestSource) Snapshot() []LivePluginReplica {
	return []LivePluginReplica{source.replica}
}
func (source metadataTestSource) HasRegisteredInstance(string) bool { return true }

func TestCoreMetadataResolutionUsesSDKClientOnly(t *testing.T) {
	registration := sdkmodels.ReplicaRegistrationRequest{Identity: sdkmodels.PeerReplicaID{InstanceID: "server", ReplicaID: "replica-1"}}
	resolver := NewRegisteredReplicaReloadResolver(metadataTestSource{replica: LivePluginReplica{Registration: registration, LeaseExpires: time.Now().Add(time.Minute)}}, func(context.Context, LivePluginReplica) (SDKReloadClient, func(), error) {
		return metadataTestClient{}, nil, nil
	}, time.Now)
	metadata, err := resolver.Metadata(context.Background(), "server")
	if err != nil || string(metadata.Manifest) != `{"id":"server"}` || !metadata.Readiness.Ready {
		t.Fatalf("metadata=%+v err=%v", metadata, err)
	}
}
