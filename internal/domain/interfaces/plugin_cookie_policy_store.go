package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type PluginCookiePolicyStore interface {
	Get(context.Context, string, string) (models.PluginCookiePolicy, error)
	List(context.Context) ([]models.PluginCookiePolicy, error)
	CompareAndSwap(context.Context, int64, models.PluginCookiePolicy, models.AuditRecord) (models.PluginCookiePolicy, error)
}
