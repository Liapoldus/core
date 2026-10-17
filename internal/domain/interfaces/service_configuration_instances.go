package interfaces

import "context"

// ServiceConfigurationInstanceLister returns instances with a retained active
// configuration. Runtime membership still comes from authenticated leases.
type ServiceConfigurationInstanceLister interface {
	ListActiveInstances(context.Context) ([]string, error)
}
