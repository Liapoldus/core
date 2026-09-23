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
}

func (service AuditService) Record(ctx context.Context, record models.AuditRecord) error {
	return service.Store.Append(ctx, record)
}

func (service AuditService) Records(ctx context.Context) ([]models.AuditRecord, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -service.RetentionDays)
	return service.Store.List(ctx, cutoff)
}
