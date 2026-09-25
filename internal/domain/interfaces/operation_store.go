package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type OperationStore interface {
	Create(context.Context, models.Operation) error
	Get(context.Context, string) (models.Operation, error)
}
