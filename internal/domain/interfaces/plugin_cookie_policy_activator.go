package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type PluginCookiePolicyActivator interface {
	ActivatePluginCookiePolicy(context.Context, models.PluginCookiePolicy) (func(context.Context) error, error)
}
