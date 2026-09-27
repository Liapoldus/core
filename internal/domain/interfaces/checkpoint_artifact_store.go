package interfaces

import "context"

type CheckpointArtifactStore interface {
	Store(context.Context, string, []byte) (string, string, error)
	Delete(context.Context, string) error
}
