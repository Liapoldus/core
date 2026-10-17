package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceRetainedConfigurationReader returns one published generation and its
// active state from a single immutable runtime view.
type ServiceRetainedConfigurationReader interface {
	Retained(context.Context, string, int64) (models.ServiceConfigurationRevision, bool, error)
}
