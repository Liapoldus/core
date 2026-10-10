package interfaces

import (
	"context"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// TrafficRolloutStore durably stores immutable rollout plans and their exact
// replica-incarnation cohorts.
type TrafficRolloutStore interface {
	CreateAndPromote(context.Context, models.TrafficRolloutSubmission) (models.Operation, bool, error)
	Create(context.Context, models.TrafficRolloutRecord) error
	Get(context.Context, string) (models.TrafficRolloutRecord, error)
	ControllerVisibleRollouts(context.Context) ([]models.TrafficRolloutRecord, error)
	OpenConfigurationRollouts(context.Context, string) ([]models.PluginConfigurationRollout, error)
	ConfigurationCohortHeld(context.Context, string, string) (bool, error)
	ConfirmStage(context.Context, string, int64, string, int, string, time.Time, models.AuditRecord) (models.TrafficRolloutRecord, error)
	ConfirmStageOnce(context.Context, string, int64, string, int, string, string, string, string, time.Time, models.AuditRecord) (models.TrafficRolloutConfirmationReceipt, bool, error)
	ApproveStage(context.Context, string, int64, string, string, time.Time, models.AuditRecord) (models.TrafficRolloutRecord, error)
	ApproveStageWithOperation(context.Context, models.OperationReservation, string, int64, string, time.Time, models.AuditRecord) (models.Operation, bool, error)
}
