package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type GroupStore interface {
	CreateApplicationGroup(context.Context, string) (models.Group, error)
	GetGroup(context.Context, string) (models.Group, error)
	CreateRevision(context.Context, models.GroupRevision) (models.GroupRevision, error)
	GetRevision(context.Context, string, string) (models.GroupRevision, error)
	GetPointers(context.Context, string) (models.GroupPointers, error)
	AdvanceCurrent(context.Context, string, string, *string) (models.GroupPointers, error)
}
