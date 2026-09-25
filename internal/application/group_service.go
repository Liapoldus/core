package application

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type GroupService struct {
	Store interfaces.GroupStore
}

func (service GroupService) List(ctx context.Context) (models.GroupList, error) {
	return service.Store.ListGroups(ctx)
}

func (service GroupService) Get(ctx context.Context, id string) (models.Group, error) {
	return service.Store.GetGroup(ctx, id)
}

func (service GroupService) Create(ctx context.Context, id string) (models.Group, error) {
	return service.Store.CreateApplicationGroup(ctx, id)
}

func (service GroupService) ListRevisions(ctx context.Context, groupID, cursor string, limit int) (models.GroupRevisionList, error) {
	return service.Store.ListRevisions(ctx, groupID, cursor, limit)
}
