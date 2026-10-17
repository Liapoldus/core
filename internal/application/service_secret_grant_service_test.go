package application

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func TestServiceSecretGrantRequiresActiveServiceAndPurposeBoundReference(t *testing.T) {
	service, references, resolved := newSecretGrantFixture()
	identity := testSecretGrantIdentity()
	for name, mutate := range map[string]func(*models.ServiceSecretReference){
		"unknown reference": func(reference *models.ServiceSecretReference) {
			reference.ID = "ssr:v1:fedcba9876543210fedcba9876543210"
		},
		"wrong service": func(reference *models.ServiceSecretReference) { reference.ServiceID = "other" },
		"wrong purpose": func(reference *models.ServiceSecretReference) { reference.Purpose = "server.tls.certificate" },
		"revoked":       func(reference *models.ServiceSecretReference) { reference.State = models.ServiceSecretReferenceRevoked },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := validGrantReference()
			mutate(&candidate)
			references.reference = candidate
			_, err := service.Issue(context.Background(), identity, validGrantReference().ID, validGrantReference().Purpose, "1")
			if !errors.Is(err, models.ServiceSecretGrantFailure{Kind: models.ServiceSecretGrantDenied}) {
				t.Fatalf("unbound reference should be denied: %v", err)
			}
		})
	}
	if *resolved {
		t.Fatal("grant issuance must not resolve secret bytes")
	}
}

func TestServiceSecretGrantRechecksRevocationBeforeRedemption(t *testing.T) {
	service, references, resolved := newSecretGrantFixture()
	identity := testSecretGrantIdentity()
	receipt, err := service.Issue(context.Background(), identity, validGrantReference().ID, validGrantReference().Purpose, "1")
	if err != nil {
		t.Fatalf("issue valid scoped grant: %v", err)
	}
	references.reference.State = models.ServiceSecretReferenceRevoked
	if _, err := service.Redeem(context.Background(), identity, receipt.Handle); !errors.Is(err, models.ServiceSecretGrantFailure{Kind: models.ServiceSecretGrantDenied}) {
		t.Fatalf("revoked reference should not redeem: %v", err)
	}
	if *resolved {
		t.Fatal("revoked reference reached the secret resolver")
	}
}

func TestServiceSecretGrantAllowsOnlyActiveReferenceAfterGenerationCheck(t *testing.T) {
	service, _, resolved := newSecretGrantFixture()
	identity := testSecretGrantIdentity()
	receipt, err := service.Issue(context.Background(), identity, validGrantReference().ID, validGrantReference().Purpose, "1")
	if err != nil {
		t.Fatalf("issue valid scoped grant: %v", err)
	}
	value, err := service.Redeem(context.Background(), identity, receipt.Handle)
	if err != nil || !bytes.Equal(value, []byte("test-secret")) || !*resolved {
		t.Fatalf("active exact-purpose reference did not redeem: %q %v resolved=%v", value, err, *resolved)
	}
	clear(value)
}

func TestServiceSecretGrantCannotBeRedeemedByAnotherReplicaIdentityOrCertificate(t *testing.T) {
	issuer := testSecretGrantIdentity()
	for name, other := range map[string]models.ServiceReplicaIdentity{
		"replica":     {InstanceID: issuer.InstanceID, ReplicaID: "replica-b", IncarnationID: issuer.IncarnationID, PlacementID: issuer.PlacementID, Fingerprint: issuer.Fingerprint},
		"incarnation": {InstanceID: issuer.InstanceID, ReplicaID: issuer.ReplicaID, IncarnationID: "incarnation-b", PlacementID: issuer.PlacementID, Fingerprint: issuer.Fingerprint},
		"placement":   {InstanceID: issuer.InstanceID, ReplicaID: issuer.ReplicaID, IncarnationID: issuer.IncarnationID, PlacementID: "remote", Fingerprint: issuer.Fingerprint},
		"certificate": {InstanceID: issuer.InstanceID, ReplicaID: issuer.ReplicaID, IncarnationID: issuer.IncarnationID, PlacementID: issuer.PlacementID, Fingerprint: "other-certificate"},
	} {
		t.Run(name, func(t *testing.T) {
			service, _, resolved := newSecretGrantFixture()
			receipt, err := service.Issue(context.Background(), issuer, validGrantReference().ID, validGrantReference().Purpose, "1")
			if err != nil {
				t.Fatalf("issue valid scoped grant: %v", err)
			}
			if _, err := service.Redeem(context.Background(), other, receipt.Handle); !errors.Is(err, models.ServiceSecretGrantFailure{Kind: models.ServiceSecretGrantDenied}) {
				t.Fatalf("grant redeemed by a different replica identity: %v", err)
			}
			if *resolved {
				t.Fatal("grant for another replica reached the secret resolver")
			}
		})
	}
}

func testSecretGrantIdentity() models.ServiceReplicaIdentity {
	return models.ServiceReplicaIdentity{
		InstanceID: "server", ReplicaID: "replica-a", IncarnationID: "incarnation-a", PlacementID: "local", Fingerprint: "replica-cert",
	}
}

type retainedSecretGrantConfiguration struct{}

func (retainedSecretGrantConfiguration) Retained(_ context.Context, instanceID string, generation int64) (models.ServiceConfigurationRevision, bool, error) {
	if instanceID != "server" || generation != 1 {
		return models.ServiceConfigurationRevision{}, false, nil
	}
	return models.ServiceConfigurationRevision{InstanceID: instanceID, Revision: 1, SettingsJSON: []byte(`{"secret":"ssr:v1:0123456789abcdef0123456789abcdef"}`)}, true, nil
}

type serviceSecretReferenceStub struct{ reference models.ServiceSecretReference }

func (store *serviceSecretReferenceStub) Create(context.Context, models.ServiceSecretReference, models.AuditRecord) (models.ServiceSecretReference, error) {
	return store.reference, nil
}
func (store *serviceSecretReferenceStub) List(context.Context, string, string) ([]models.ServiceSecretReference, error) {
	return []models.ServiceSecretReference{store.reference}, nil
}
func (store *serviceSecretReferenceStub) Get(_ context.Context, serviceID, referenceID string) (models.ServiceSecretReference, error) {
	if store.reference.ID != referenceID {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceNotFound{}
	}
	if serviceID != "server" {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceNotFound{}
	}
	return store.reference, nil
}
func (store *serviceSecretReferenceStub) Revoke(context.Context, string, string, models.AuditRecord) error {
	store.reference.State = models.ServiceSecretReferenceRevoked
	return nil
}

func newSecretGrantFixture() (*ServiceSecretGrantService, *serviceSecretReferenceStub, *bool) {
	resolved := false
	references := &serviceSecretReferenceStub{reference: validGrantReference()}
	return &ServiceSecretGrantService{
		Configurations: retainedSecretGrantConfiguration{}, References: references,
		ResolveSecret: func(context.Context, string, int64) ([]byte, error) {
			resolved = true
			return []byte("test-secret"), nil
		},
		GrantTTL: time.Minute, MaximumValueBytes: 128, MaximumOutstanding: 8,
		MaximumReferenceLen: 128, MaximumPurposeLen: 128, Now: func() time.Time { return time.Unix(1, 0) },
		Random: bytes.NewReader(bytes.Repeat([]byte{7}, 64)),
	}, references, &resolved
}

func validGrantReference() models.ServiceSecretReference {
	return models.ServiceSecretReference{
		ID: "ssr:v1:0123456789abcdef0123456789abcdef", ServiceID: "server", Purpose: "server.http.auth.bearer",
		Label: "Test secret", State: models.ServiceSecretReferenceActive, CreatedAt: "2026-10-17T00:00:00Z",
	}
}
