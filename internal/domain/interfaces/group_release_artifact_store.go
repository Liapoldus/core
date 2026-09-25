package interfaces

import (
	"context"

	"github.com/Liapoldus/core/internal/domain/models"
)

type GroupReleaseArtifactStore interface {
	StageCaddyfile(context.Context, string, []byte) (models.GroupRevision, error)
	DiscardCaddyfile(context.Context, models.GroupRevision) error
}
