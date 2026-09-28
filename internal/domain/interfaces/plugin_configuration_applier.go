package interfaces

import "context"

type PluginConfigurationApplier interface {
	ApplyConfiguration(context.Context, string, string, []byte) error
}
