package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceConfigurationRolloutStore atomically changes active configuration
// pointers with an immutable target cohort and stores per-incarnation ACKs.
type ServiceConfigurationRolloutStore interface {
	ActivateCandidateWithTargets(context.Context, string, string, int64, int64, models.AuditRecord, []models.ServiceRolloutTarget) (models.ServiceConfigurationPointers, error)
	RestorePreviousWithTargets(context.Context, string, string, string, int64, models.AuditRecord, []models.ServiceRolloutTarget) (models.ServiceConfigurationPointers, error)
	Targets(context.Context, string) ([]models.ServiceRolloutTarget, bool, error)
	AcknowledgeTargets(context.Context, string, []models.ServiceRolloutTarget) error
	CompleteRollout(context.Context, string) error
	CloseRollout(context.Context, string) error
	FailRolloutTargetLost(context.Context, string, []models.ServiceRolloutTarget, string, string, string, string) error
}
