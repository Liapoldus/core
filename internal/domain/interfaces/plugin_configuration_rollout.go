package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// PluginConfigurationRolloutStore atomically changes active configuration
// pointers with an immutable target cohort and stores per-incarnation ACKs.
type PluginConfigurationRolloutStore interface {
	ActivateCandidateWithTargets(context.Context, string, string, int64, int64, models.AuditRecord, []models.PluginRolloutTarget) (models.PluginConfigurationPointers, error)
	RestorePreviousWithTargets(context.Context, string, string, string, int64, models.AuditRecord, []models.PluginRolloutTarget) (models.PluginConfigurationPointers, error)
	Targets(context.Context, string) ([]models.PluginRolloutTarget, bool, error)
	AcknowledgeTargets(context.Context, string, []models.PluginRolloutTarget) error
	CompleteRollout(context.Context, string) error
	CloseRollout(context.Context, string) error
	FailRolloutTargetLost(context.Context, string, []models.PluginRolloutTarget, string, string, string, string) error
}
