package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

// PluginRetainedConfigurationReader returns one published generation and its
// active state from a single immutable runtime view.
type PluginRetainedConfigurationReader interface {
	Retained(context.Context, string, int64) (models.PluginConfigurationRevision, bool, error)
}
