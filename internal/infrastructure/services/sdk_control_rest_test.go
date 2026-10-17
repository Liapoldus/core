package services

import (
	"context"
	"errors"
	"testing"
)

func TestRESTResolutionRequiresRegisteredReplica(t *testing.T) {
	applier := &SDKConfigurationApplier{}
	if _, _, found, err := applier.resolve(context.Background(), "neutral-instance"); found || !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("unregistered resolution: found=%v err=%v", found, err)
	}
	if _, err := applier.TrafficRolloutReplicas(context.Background(), "neutral-instance"); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("unregistered cohort: %v", err)
	}
	if err := applier.ValidateTrafficRolloutConfiguration(context.Background(), "neutral-instance", []byte(`{}`)); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("unregistered validation: %v", err)
	}
}
