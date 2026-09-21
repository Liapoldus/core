package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type PolicyEngine interface {
	Evaluate(any) (models.PolicyDecision, error)
}
