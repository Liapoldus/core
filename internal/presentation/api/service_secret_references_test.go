package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
)

func TestManagementServiceSecretReferenceLifecycleExposesMetadataOnly(t *testing.T) {
	management, _ := config.LoadManagement()
	audit, _ := config.LoadAudit()
	errors, _ := config.LoadErrorCatalog()
	store := &serviceSecretReferenceStoreFixture{}
	server := &Server{
		Management: management, AuditWords: audit, Errors: errors, Token: "test-token",
		ServiceSecretReferences: &application.ServiceSecretReferenceService{Store: store},
	}
	base := management.Paths.Services + "/server/" + management.Paths.ServiceSecretsSuffix
	validID := "ssr:v1:0123456789abcdef0123456789abcdef"

	create := httptest.NewRequest(http.MethodPost, base, strings.NewReader(`{"reference":"`+validID+`","purpose":"server.http.auth.bearer","label":"Production API"}`))
	create.Header.Set("Authorization", "Bearer test-token")
	create.Header.Set("Content-Type", "application/json")
	created := httptest.NewRecorder()
	server.Handler().ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var metadata models.ServiceSecretReference
	if err := json.Unmarshal(created.Body.Bytes(), &metadata); err != nil || metadata.ID != validID || metadata.ServiceID != "server" {
		t.Fatalf("created response did not return the safe reference metadata: %+v, %v", metadata, err)
	}
	if store.createAudit.Action != audit.Audit.Actions.ServiceSecretCreate || strings.Contains(created.Body.String(), "secret-value") {
		t.Fatal("creation response or audit did not remain metadata-only")
	}

	list := httptest.NewRequest(http.MethodGet, base+"?purpose=server.http.auth.bearer", nil)
	list.Header.Set("Authorization", "Bearer test-token")
	listed := httptest.NewRecorder()
	server.Handler().ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), validID) || !strings.Contains(listed.Body.String(), "Production API") {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}

	revoke := httptest.NewRequest(http.MethodDelete, base+"/"+validID, nil)
	revoke.Header.Set("Authorization", "Bearer test-token")
	revoked := httptest.NewRecorder()
	server.Handler().ServeHTTP(revoked, revoke)
	if revoked.Code != http.StatusNoContent || store.reference.State != models.ServiceSecretReferenceRevoked ||
		store.revokeAudit.Action != audit.Audit.Actions.ServiceSecretRevoke {
		t.Fatalf("revoke status=%d state=%s audit=%+v body=%s", revoked.Code, store.reference.State, store.revokeAudit, revoked.Body.String())
	}
}

func TestManagementServiceSecretReferenceRejectsSecretValues(t *testing.T) {
	management, _ := config.LoadManagement()
	audit, _ := config.LoadAudit()
	errors, _ := config.LoadErrorCatalog()
	store := &serviceSecretReferenceStoreFixture{}
	server := &Server{
		Management: management, AuditWords: audit, Errors: errors, Token: "test-token",
		ServiceSecretReferences: &application.ServiceSecretReferenceService{Store: store},
	}
	base := management.Paths.Services + "/server/" + management.Paths.ServiceSecretsSuffix
	request := httptest.NewRequest(http.MethodPost, base, strings.NewReader(`{"reference":"ssr:v1:0123456789abcdef0123456789abcdef","purpose":"server.http.auth.bearer","label":"Production API","value":"never-accept-this"}`))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || store.created || strings.Contains(response.Body.String(), "never-accept-this") {
		t.Fatalf("secret-bearing request was not rejected safely: status=%d body=%s created=%v", response.Code, response.Body.String(), store.created)
	}
}

type serviceSecretReferenceStoreFixture struct {
	reference   models.ServiceSecretReference
	createAudit models.AuditRecord
	revokeAudit models.AuditRecord
	created     bool
}

func (store *serviceSecretReferenceStoreFixture) Create(_ context.Context, reference models.ServiceSecretReference, audit models.AuditRecord) (models.ServiceSecretReference, error) {
	store.reference, store.createAudit, store.created = reference, audit, true
	store.reference.CreatedAt = "2026-10-17T00:00:00Z"
	return store.reference, nil
}
func (store *serviceSecretReferenceStoreFixture) List(_ context.Context, serviceID, purpose string) ([]models.ServiceSecretReference, error) {
	if store.reference.ID == "" || store.reference.ServiceID != serviceID || purpose != "" && store.reference.Purpose != purpose {
		return []models.ServiceSecretReference{}, nil
	}
	return []models.ServiceSecretReference{store.reference}, nil
}
func (store *serviceSecretReferenceStoreFixture) Get(_ context.Context, serviceID, referenceID string) (models.ServiceSecretReference, error) {
	if store.reference.ServiceID != serviceID || store.reference.ID != referenceID {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceNotFound{}
	}
	return store.reference, nil
}
func (store *serviceSecretReferenceStoreFixture) Revoke(_ context.Context, serviceID, referenceID string, audit models.AuditRecord) error {
	if store.reference.ServiceID != serviceID || store.reference.ID != referenceID {
		return models.ServiceSecretReferenceNotFound{}
	}
	store.reference.State, store.revokeAudit = models.ServiceSecretReferenceRevoked, audit
	return nil
}
