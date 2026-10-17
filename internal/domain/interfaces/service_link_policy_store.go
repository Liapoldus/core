package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceLinkPolicyStore persists Core-owned caller→target link policies. Every
// mutation is atomic with its audit record and uses the pair's monotonic
// revision for optimistic concurrency.
type ServiceLinkPolicyStore interface {
	ServiceLinkPolicyReader
	Create(context.Context, models.ServiceLinkPolicy, models.AuditRecord) (models.ServiceLinkPolicy, error)
	Replace(context.Context, string, string, int64, models.ServiceLinkPolicy, models.AuditRecord) (models.ServiceLinkPolicy, error)
	Delete(context.Context, string, string, int64, models.AuditRecord) error
}
