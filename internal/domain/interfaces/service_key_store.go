package interfaces

import "context"

import "github.com/Liapoldus/core/v3/internal/domain/models"

type ServiceKeyStore interface {
	Bootstrap(context.Context, string, []byte, string) error
	Create(context.Context, models.ServiceKey, models.AuditRecord) error
	ActiveVerifiers(context.Context) ([]models.ServiceKeyVerifier, error)
	Metadata(context.Context) ([]models.ServiceKeyMetadata, error)
}
