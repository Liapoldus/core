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

func (service OperationService) Get(ctx context.Context, id string) (models.Operation, error) {
	return service.Store.Get(ctx, id)
}
