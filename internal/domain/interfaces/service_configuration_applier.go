package interfaces

import "context"

type ServiceConfigurationApplier interface {
	ApplyConfiguration(context.Context, string, string, []byte) error
}
