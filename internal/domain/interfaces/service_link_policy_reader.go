package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceLinkPolicyReader reads durable, Core-owned caller→target link policies.
type ServiceLinkPolicyReader interface {
	List(context.Context) ([]models.ServiceLinkPolicy, error)
	Get(context.Context, string, string) (models.ServiceLinkPolicy, error)
}
