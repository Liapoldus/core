package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type PolicyEngine interface {
	Evaluate(models.PolicyInput) (models.PolicyDecision, error)
}
