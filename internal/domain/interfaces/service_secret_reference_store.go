package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceSecretReferenceStore persists safe catalog metadata only. It never
// accepts secret bytes, storage locators or grant handles.
type ServiceSecretReferenceStore interface {
	Create(context.Context, models.ServiceSecretReference, models.AuditRecord) (models.ServiceSecretReference, error)
	List(context.Context, string, string) ([]models.ServiceSecretReference, error)
	Get(context.Context, string, string) (models.ServiceSecretReference, error)
	Revoke(context.Context, string, string, models.AuditRecord) error
}
