package interfaces

import (
	"context"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// OperationReplicaOutcomeReader exposes configuration rollout results without
// widening the durable operation payload or exposing replica transport data.
type OperationReplicaOutcomeReader interface {
	ReplicaOutcomes(context.Context, string, string, string) ([]models.OperationReplicaOutcome, error)
}
