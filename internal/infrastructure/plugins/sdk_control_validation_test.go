package plugins

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
)

type validationReplicaSource struct{ live LivePluginReplica }

func (source validationReplicaSource) Snapshot() []LivePluginReplica {
	return []LivePluginReplica{source.live}
}

func (validationReplicaSource) HasRegisteredInstance(string) bool { return true }

type validationReloadClient struct{}

func (validationReloadClient) Reload(context.Context, sdkmodels.Reload) (sdkmodels.ReloadAcknowledgement, error) {
	return sdkmodels.ReloadAcknowledgement{}, nil
}

func (validationReloadClient) ConfigurationSchema(context.Context) ([]byte, error) {
	return []byte(`{"type":"object"}`), nil
}

func TestRegisteredRolloutRequiresInjectedSchemaValidator(t *testing.T) {
	now := time.Now().UTC()
	live := LivePluginReplica{LeaseExpires: now.Add(time.Minute)}
	live.Registration.Identity.InstanceID = "instance"
	live.Registration.Ready = true
	releases := 0
	resolver := NewRegisteredReplicaReloadResolver(validationReplicaSource{live: live},
		func(context.Context, LivePluginReplica) (SDKReloadClient, func(), error) {
			return validationReloadClient{}, func() { releases++ }, nil
		}, func() time.Time { return now })
	ctx := context.Background()
	document := []byte(`{"value":1}`)
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", document); !errors.Is(err, ErrPluginUnavailable) {
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
	var conflict models.PluginConfigurationConflict
	if err := resolver.ValidateTrafficRolloutConfiguration(ctx, "instance", document); !errors.As(err, &conflict) {
		t.Fatalf("invalid document must reject rollout: %v", err)
	}
	if releases != 2 {
		t.Fatal("rejected configuration must release the client")
	}
}
