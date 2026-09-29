package application

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type OperationService struct {
	Store interfaces.OperationStore
}

func (service OperationService) Create(ctx context.Context, operation models.Operation) error {
	return service.Store.Create(ctx, operation)
}

func (service OperationService) Reserve(ctx context.Context, reservation models.OperationReservation) (models.Operation, bool, error) {
	return service.Store.Reserve(ctx, reservation)
}

func (service OperationService) Transition(ctx context.Context, id, fromState, toState, errorCode string) error {
	return service.Store.Transition(ctx, id, fromState, toState, errorCode)
}

func (service OperationService) FindByIdempotency(ctx context.Context, actor, scope, key, requestDigest string) (models.Operation, bool, error) {
	return service.Store.FindByIdempotency(ctx, actor, scope, key, requestDigest)
}

func (service OperationService) ListRecoverable(ctx context.Context, kind, pendingState, runningState string) ([]models.Operation, error) {
	return service.Store.ListRecoverable(ctx, kind, pendingState, runningState)
}

func (service OperationService) Get(ctx context.Context, id string) (models.Operation, error) {
	return service.Store.Get(ctx, id)
}
