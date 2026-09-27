package interfaces

import "context"

type GroupReleaseDriftGuard interface {
	Drifted(context.Context) (bool, error)
}
