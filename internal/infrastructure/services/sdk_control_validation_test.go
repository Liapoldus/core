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

type validationReloadClient struct {
	generations []string
	schemaErr   error
	validateErr error
}

func (client *validationReloadClient) ValidateConfiguration(_ context.Context, generation string, _ []byte) error {
	client.generations = append(client.generations, generation)
	return client.validateErr
}

func (*validationReloadClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
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

func (client *validationReloadClient) ConfigurationSchema(context.Context) ([]byte, error) {
	if client.schemaErr != nil {
		return nil, client.schemaErr
	}
	return []byte(`{"type":"object"}`), nil
}

func TestRegisteredRolloutRequiresInjectedSchemaValidator(t *testing.T) {
	now := time.Now().UTC()
	live := LiveServiceReplica{LeaseExpires: now.Add(time.Minute)}
	live.Registration.Identity.InstanceID = "instance"
	live.Registration.Ready = true
	releases := 0
	validationClient := &validationReloadClient{}
	resolver := NewRegisteredReplicaReloadResolver(validationReplicaSource{live: live},
		func(context.Context, LiveServiceReplica) (SDKReloadClient, func(), error) {
			return validationClient, func() { releases++ }, nil
		}, func() time.Time { return now })
	ctx := context.Background()
	document := []byte(`{"value":1}`)
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", "", document); !errors.Is(err, ErrServiceUnavailable) {
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
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", "17", document); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || releases != 1 || len(validationClient.generations) != 1 || validationClient.generations[0] != "17" {
		t.Fatalf("calls/releases/generations = %d/%d/%v", calls, releases, validationClient.generations)
	}
	resolver.ValidateSchema = func([]byte, []byte) error { return errors.New("invalid configuration") }
	var validationFailure models.ServiceConfigurationCandidateValidationFailed
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", "17", document); !errors.As(err, &validationFailure) {
		t.Fatalf("invalid document must fail candidate validation: %v", err)
	}
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", "17", document); !errors.As(err, &validationFailure) ||
		strings.Contains(err.Error(), "invalid configuration") {
		t.Fatalf("private schema detail escaped candidate validation failure: %v", err)
	}
	validationClient.validateErr = errors.New("private secret grant detail")
	resolver.ValidateSchema = func([]byte, []byte) error { return nil }
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", "17", document); !errors.As(err, &validationFailure) ||
		strings.Contains(err.Error(), "private secret grant detail") {
		t.Fatalf("private validator detail escaped candidate validation failure: %v", err)
	}
	validationClient.validateErr = nil
	validationClient.schemaErr = errors.New("private schema transport detail")
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", "17", document); !errors.Is(err, ErrServiceUnavailable) ||
		strings.Contains(err.Error(), "private schema transport detail") {
		t.Fatalf("schema transport error was not safely classified: %v", err)
	}
	if releases != 5 {
		t.Fatal("rejected configuration must release the client")
	}
}
