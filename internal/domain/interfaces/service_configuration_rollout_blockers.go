package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceConfigurationRolloutBlockerReader reports safe, stable reasons that
// currently prevent a new configuration candidate from being created.
type ServiceConfigurationRolloutBlockerReader interface {
	RolloutBlockers(context.Context, string) ([]models.ConfigurationBlocker, error)
}
