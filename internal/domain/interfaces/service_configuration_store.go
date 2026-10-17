package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type ServiceConfigurationStore interface {
	ServiceConfigurationReader
	CreateCandidate(context.Context, string, string, string, string, int64, int64, []byte, models.AuditRecord) (models.ServiceConfigurationRevision, error)
	ActivateCandidate(context.Context, string, int64, int64, models.AuditRecord) (models.ServiceConfigurationPointers, error)
	FailCandidate(context.Context, string, int64, int64, models.AuditRecord) (models.ServiceConfigurationPointers, error)
	RestorePrevious(context.Context, string, int64, models.AuditRecord) (models.ServiceConfigurationPointers, error)
}
