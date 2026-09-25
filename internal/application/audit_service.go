package application

import (
	"context"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type AuditService struct {
	Store         interfaces.AuditStore
	RetentionDays int
	MinimumLimit  int
	DefaultLimit  int
	MaximumLimit  int
	InvalidLimit  string
}

func (service AuditService) Record(ctx context.Context, record models.AuditRecord) error {
	return service.Store.Append(ctx, record)
}

func (service AuditService) Records(ctx context.Context, cursor string, limit int) (models.AuditPage, error) {
	if limit == 0 {
		limit = service.DefaultLimit
	}
	if limit < service.MinimumLimit || limit > service.MaximumLimit {
		return models.AuditPage{}, models.AuditPageError{Message: service.InvalidLimit}
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -service.RetentionDays)
	return service.Store.List(ctx, cutoff, cursor, limit)
}
