package runtime

import (
	"database/sql"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/services"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
	sdkmodels "github.com/Liapoldus/service-sdk/v2/domain/models"
)

func TestRegisteredInstanceConvergedRequiresCurrentIncarnationObservation(t *testing.T) {
	const (
		instanceID = "server"
		replicaID  = "replica-1"
	)
	convergence := storage.ServiceConvergenceView{
		Desired: map[string]int64{instanceID: 9},
		Records: []storage.ServiceReplicaObservation{{
			InstanceID: instanceID, ReplicaID: replicaID, IncarnationID: "old-incarnation",
			ObservedGeneration: sql.NullInt64{Int64: 9, Valid: true},
			ObservedState:      storage.ReplicaObservedAcknowledged,
		}},
	}
	live := []services.LiveServiceReplica{{Registration: sdkmodels.ReplicaRegistrationRequest{
		Identity: sdkmodels.PeerReplicaID{InstanceID: instanceID, ReplicaID: replicaID, IncarnationID: "new-incarnation"},
	}}}

	ready, drifted := registeredInstanceConverged(instanceID, convergence, live)
	if ready || !drifted {
		t.Fatalf("old incarnation observation: ready=%v drifted=%v, want false/true", ready, drifted)
	}

	convergence.Records[0].IncarnationID = "new-incarnation"
	ready, drifted = registeredInstanceConverged(instanceID, convergence, live)
	if !ready || drifted {
		t.Fatalf("current incarnation observation: ready=%v drifted=%v, want true/false", ready, drifted)
	}
}

func TestServiceReplicaConfigurationStatusUsesExactIncarnationAndGeneration(t *testing.T) {
	const instanceID, replicaID, incarnationID = "server", "replica-1", "incarnation-current"
	desired := int64(7)
	convergence := storage.ServiceConvergenceView{Desired: map[string]int64{instanceID: desired}, Records: []storage.ServiceReplicaObservation{
		{InstanceID: instanceID, ReplicaID: replicaID, IncarnationID: "incarnation-old", ObservedState: storage.ReplicaObservedAcknowledged, ObservedGeneration: sql.NullInt64{Int64: desired, Valid: true}},
	}}
	status := serviceReplicaConfigurationStatus(convergence, instanceID, replicaID, incarnationID)
	if status.State != models.ServiceConfigurationUnobserved || status.DesiredGeneration == nil || *status.DesiredGeneration != desired || status.AppliedGeneration != nil {
		t.Fatalf("old incarnation leaked into status: %+v", status)
	}
	convergence.Records = []storage.ServiceReplicaObservation{{
		InstanceID: instanceID, ReplicaID: replicaID, IncarnationID: incarnationID,
		ObservedState: storage.ReplicaObservedAcknowledged, ObservedGeneration: sql.NullInt64{Int64: 6, Valid: true},
	}}
	status = serviceReplicaConfigurationStatus(convergence, instanceID, replicaID, incarnationID)
	if status.State != models.ServiceConfigurationStale || status.AppliedGeneration == nil || *status.AppliedGeneration != 6 {
		t.Fatalf("stale generation was not surfaced: %+v", status)
	}
	convergence.Records[0].ObservedGeneration = sql.NullInt64{Int64: desired, Valid: true}
	status = serviceReplicaConfigurationStatus(convergence, instanceID, replicaID, incarnationID)
	if status.State != models.ServiceConfigurationAcknowledged || status.AppliedGeneration == nil || *status.AppliedGeneration != desired {
		t.Fatalf("current generation was not acknowledged: %+v", status)
	}
}
