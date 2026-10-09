package interfaces

import "context"

// PluginConfigurationInstanceLister returns instances with a retained active
// configuration. Runtime membership still comes from authenticated leases.
type PluginConfigurationInstanceLister interface {
	ListActiveInstances(context.Context) ([]string, error)
}
