package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type OperationStore interface {
	Create(context.Context, models.Operation) error
	Reserve(context.Context, models.OperationReservation) (models.Operation, bool, error)
	Transition(context.Context, string, string, string, string) error
	FindByIdempotency(context.Context, string, string, string, string) (models.Operation, bool, error)
	ListRecoverable(context.Context, string, string, string) ([]models.Operation, error)
	Get(context.Context, string) (models.Operation, error)
	Payload(context.Context, string) (models.OperationPayload, bool, error)
}
