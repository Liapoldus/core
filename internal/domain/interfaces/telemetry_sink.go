package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type TelemetrySink interface {
	Record(models.TelemetryEvent)
}
