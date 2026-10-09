package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

// PluginConfigurationRolloutApplier captures the current authenticated
// registration set once and applies a generation only to that exact set.
type PluginConfigurationRolloutApplier interface {
	CaptureConfigurationTargets(context.Context, string) ([]models.PluginRolloutTarget, bool, error)
	LostConfigurationTargets(context.Context, string, []models.PluginRolloutTarget) ([]models.PluginRolloutTarget, error)
	ApplyConfigurationToTargets(context.Context, string, string, string, []byte, []models.PluginRolloutTarget) ([]models.PluginRolloutTarget, error)
}
