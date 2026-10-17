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

type validationReplicaSource struct{ live LiveServiceReplica }

func (source validationReplicaSource) Snapshot() []LiveServiceReplica {
	return []LiveServiceReplica{source.live}
}

func (validationReplicaSource) HasRegisteredInstance(string) bool { return true }

type validationReloadClient struct{}

func (validationReloadClient) ValidateConfiguration(context.Context, []byte) error { return nil }

func (validationReloadClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}

type mismatchedReadinessClient struct{ reloadCalled bool }

func (client *mismatchedReadinessClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	client.reloadCalled = true
	return sdkmodels.ReloadAcknowledgement{}, nil
}

func (*mismatchedReadinessClient) Readiness(context.Context) (sdkmodels.Readiness, error) {
	return sdkmodels.Readiness{InstanceID: "instance", ReplicaID: "different-replica", Ready: true}, nil
}

func TestReloadObservedRetainsIncarnationWhenLiveIdentityMismatches(t *testing.T) {
	client := &mismatchedReadinessClient{}
	fanout := SDKReloadFanout{InstanceID: "instance", Replicas: []SDKReloadReplicaClient{{
		ReplicaID: "replica-1", IncarnationID: "incarnation-2", Client: client,
	}}}
	_, results, err := fanout.ReloadObserved(context.Background(), sdkmodels.Reload{
		Generation: "1", SHA256: strings.Repeat("a", 64), SchemaVersion: "1",
	})
	if !errors.Is(err, ErrProtocolViolation) || len(results) != 1 || results[0].ReplicaID != "replica-1" ||
		results[0].IncarnationID != "incarnation-2" || results[0].Acknowledged || results[0].Unreachable || client.reloadCalled {
		t.Fatalf("identity mismatch outcome lost safe replica identity: results=%+v err=%v reload=%v", results, err, client.reloadCalled)
	}
}

func (validationReloadClient) ConfigurationSchema(context.Context) ([]byte, error) {
	return []byte(`{"type":"object"}`), nil
}

func TestRegisteredRolloutRequiresInjectedSchemaValidator(t *testing.T) {
	now := time.Now().UTC()
	live := LiveServiceReplica{LeaseExpires: now.Add(time.Minute)}
	live.Registration.Identity.InstanceID = "instance"
	live.Registration.Ready = true
	releases := 0
	resolver := NewRegisteredReplicaReloadResolver(validationReplicaSource{live: live},
		func(context.Context, LiveServiceReplica) (SDKReloadClient, func(), error) {
			return validationReloadClient{}, func() { releases++ }, nil
		}, func() time.Time { return now })
	ctx := context.Background()
	document := []byte(`{"value":1}`)
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", document); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("missing validator must fail closed: %v", err)
	}
	if releases != 0 {
		t.Fatal("missing validator must not create a client")
	}
	calls := 0
	resolver.ValidateSchema = func(actual, schema []byte) error {
		calls++
		if string(actual) != string(document) || string(schema) != `{"type":"object"}` {
			t.Fatal("validator did not receive original document and replica schema")
		}
		return nil
	}
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", document); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || releases != 1 {
		t.Fatalf("calls/releases = %d/%d", calls, releases)
	}
	resolver.ValidateSchema = func([]byte, []byte) error { return errors.New("invalid configuration") }
	var conflict models.ServiceConfigurationConflict
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", document); !errors.As(err, &conflict) {
		t.Fatalf("invalid document must reject rollout: %v", err)
	}
	if releases != 2 {
		t.Fatal("rejected configuration must release the client")
	}
}
