package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceStagingConfigurationReader exposes only the exact candidate currently
// held in Core's private staging slot. Implementations must not make it
// available through the normal replica configuration-pull reader.
type ServiceStagingConfigurationReader interface {
	Staged(context.Context, string, int64) (models.ServiceConfigurationRevision, bool, error)
}
