package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type CaddyAdminClient interface {
	Snapshot(context.Context) ([]byte, error)
	Request(context.Context, models.CaddyAdminRequest) (models.CaddyAdminResponse, error)
}
