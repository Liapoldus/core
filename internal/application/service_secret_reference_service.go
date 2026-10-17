package application

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceSecretReferenceService exposes only service- and purpose-bound safe
// metadata to the Management API and scoped Service SDK grant path.
type ServiceSecretReferenceService struct {
	Store interfaces.ServiceSecretReferenceStore
}

func (service *ServiceSecretReferenceService) Create(
	ctx context.Context,
	reference models.ServiceSecretReference,
	audit models.AuditRecord,
) (models.ServiceSecretReference, error) {
	if service == nil || service.Store == nil {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceUnavailable{}
	}
	if reference.Validate() != nil || reference.State != models.ServiceSecretReferenceActive {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceInvalid{}
	}
	return service.Store.Create(ctx, reference, audit)
}

func (service *ServiceSecretReferenceService) List(ctx context.Context, serviceID, purpose string) ([]models.ServiceSecretReference, error) {
	if service == nil || service.Store == nil || serviceID == "" {
		return nil, models.ServiceSecretReferenceUnavailable{}
	}
	return service.Store.List(ctx, serviceID, purpose)
}

func (service *ServiceSecretReferenceService) Get(ctx context.Context, serviceID, referenceID string) (models.ServiceSecretReference, error) {
	if service == nil || service.Store == nil || serviceID == "" || referenceID == "" {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceUnavailable{}
	}
	return service.Store.Get(ctx, serviceID, referenceID)
}

func (service *ServiceSecretReferenceService) Revoke(
	ctx context.Context,
	serviceID, referenceID string,
	audit models.AuditRecord,
) error {
	if service == nil || service.Store == nil || serviceID == "" || referenceID == "" {
		return models.ServiceSecretReferenceUnavailable{}
	}
	return service.Store.Revoke(ctx, serviceID, referenceID, audit)
}
