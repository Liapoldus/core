package interfaces

import (
	"context"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
)

type AuditStore interface {
	Append(context.Context, models.AuditRecord) error
	List(context.Context, time.Time) ([]models.AuditRecord, error)
}
