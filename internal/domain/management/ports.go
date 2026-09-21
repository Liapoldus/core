// Package management defines control-plane authorization and auditing ports.
package management

type Actor struct {
	ID string
}

type Authorizer interface {
	Authorize(Actor, string) error
}
