package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type PluginConfigurationStore interface {
	PluginConfigurationReader
	CreateCandidate(context.Context, string, string, string, string, int64, int64, []byte, models.AuditRecord) (models.PluginConfigurationRevision, error)
	ActivateCandidate(context.Context, string, int64, int64, models.AuditRecord) (models.PluginConfigurationPointers, error)
	FailCandidate(context.Context, string, int64, int64, models.AuditRecord) (models.PluginConfigurationPointers, error)
	RestorePrevious(context.Context, string, int64, models.AuditRecord) (models.PluginConfigurationPointers, error)
}
