package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type GroupReleaseStore interface {
	Reserve(context.Context, models.GroupReleaseReservation) (models.Operation, bool, error)
	Commit(context.Context, models.GroupReleaseCommit) error
	Fail(context.Context, string, string, string, models.AuditRecord) error
	Pending(context.Context) ([]models.GroupReleaseReservation, error)
}
