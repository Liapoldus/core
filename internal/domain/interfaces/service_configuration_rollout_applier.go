package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceConfigurationRolloutApplier captures the current authenticated
// registration set once and applies a generation only to that exact set.
type ServiceConfigurationRolloutApplier interface {
	CaptureConfigurationTargets(context.Context, string) ([]models.ServiceRolloutTarget, bool, error)
	LostConfigurationTargets(context.Context, string, []models.ServiceRolloutTarget) ([]models.ServiceRolloutTarget, error)
	ApplyConfigurationToTargets(context.Context, string, string, string, []byte, []models.ServiceRolloutTarget) ([]models.ServiceRolloutTarget, error)
}
