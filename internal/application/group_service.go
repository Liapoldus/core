package application

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type GroupService struct {
	Store         interfaces.GroupStore
	ContentReader interfaces.GroupRevisionContentReader
}

func (service GroupService) List(ctx context.Context) (models.GroupList, error) {
	return service.Store.ListGroups(ctx)
}

func (service GroupService) Get(ctx context.Context, id string) (models.Group, error) {
	return service.Store.GetGroup(ctx, id)
}

func (service GroupService) Create(ctx context.Context, id string, record models.AuditRecord) (models.Group, error) {
	return service.Store.CreateApplicationGroup(ctx, id, record)
}

func (service GroupService) ListRevisions(ctx context.Context, groupID, cursor string, limit int) (models.GroupRevisionList, error) {
	return service.Store.ListRevisions(ctx, groupID, cursor, limit)
}

func (service GroupService) GetRevisionDetail(ctx context.Context, groupID, revisionID string) (models.GroupRevisionDetail, error) {
	revision, err := service.Store.GetRevision(ctx, groupID, revisionID)
	if err != nil {
		return models.GroupRevisionDetail{}, err
	}
	return service.ContentReader.Read(ctx, revision)
}
