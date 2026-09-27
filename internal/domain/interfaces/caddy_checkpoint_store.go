package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type CaddyCheckpointStore interface {
	CreateMutation(context.Context, models.CaddyCheckpoint, models.Operation, models.AuditRecord) error
	CompleteMutation(context.Context, string, string, models.AuditRecord) error
}
