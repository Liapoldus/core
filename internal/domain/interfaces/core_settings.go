package interfaces

import (
	"context"
	"github.com/Liapoldus/core/internal/domain/models"
)

// CoreSettings owns persistence/CAS, never runtime transport or ENV access.
type CoreSettings interface {
	Read(context.Context) (models.CoreSettingsSnapshot, error)
	Update(context.Context, int64, []byte, string) (models.CoreSettingsSnapshot, error)
}
