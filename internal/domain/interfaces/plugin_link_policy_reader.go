package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

// PluginLinkPolicyReader reads durable, Core-owned caller→target link policies.
type PluginLinkPolicyReader interface {
	List(context.Context) ([]models.PluginLinkPolicy, error)
	Get(context.Context, string, string) (models.PluginLinkPolicy, error)
}
