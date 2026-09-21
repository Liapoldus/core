// Package upstream defines endpoint resolution and balancing ports.
package upstream

type Target struct{}

type Resolver interface {
	Resolve(name string) ([]Target, error)
}
