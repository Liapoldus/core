package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

// PluginConfigurationReader exposes only retained, pullable generations.
// Infrastructure may implement it from an immutable in-memory snapshot.
type PluginConfigurationReader interface {
	Current(context.Context, string) (models.PluginConfigurationRevision, models.PluginConfigurationPointers, error)
	GetRevision(context.Context, string, int64) (models.PluginConfigurationRevision, error)
}
