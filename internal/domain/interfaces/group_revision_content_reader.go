package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type GroupRevisionContentReader interface {
	Read(context.Context, models.GroupRevision) (models.GroupRevisionDetail, error)
}
