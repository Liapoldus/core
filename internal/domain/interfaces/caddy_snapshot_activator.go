package interfaces

import "context"

type CaddySnapshotActivator interface {
	Validate(context.Context, []byte) error
	Activate(context.Context, []byte) error
}
