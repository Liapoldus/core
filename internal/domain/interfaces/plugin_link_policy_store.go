package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// PluginLinkPolicyStore persists Core-owned caller→target link policies. Every
// mutation is atomic with its audit record and uses the pair's monotonic
// revision for optimistic concurrency.
type PluginLinkPolicyStore interface {
	PluginLinkPolicyReader
	Create(context.Context, models.PluginLinkPolicy, models.AuditRecord) (models.PluginLinkPolicy, error)
	Replace(context.Context, string, string, int64, models.PluginLinkPolicy, models.AuditRecord) (models.PluginLinkPolicy, error)
	Delete(context.Context, string, string, int64, models.AuditRecord) error
}
