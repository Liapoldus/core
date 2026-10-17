package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceConfigurationReader exposes only retained, pullable generations.
// Infrastructure may implement it from an immutable in-memory snapshot.
type ServiceConfigurationReader interface {
	Current(context.Context, string) (models.ServiceConfigurationRevision, models.ServiceConfigurationPointers, error)
	GetRevision(context.Context, string, int64) (models.ServiceConfigurationRevision, error)
}
