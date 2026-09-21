// Package plugin defines supervised capability sessions.
package plugin

type Capability struct {
	Name string
}

type Supervisor interface {
	Restart(instance string) error
}
