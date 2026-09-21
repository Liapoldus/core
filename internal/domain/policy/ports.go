// Package policy defines authorization and traffic decision ports.
package policy

type Decision uint8

type Engine interface {
	Evaluate(any) (Decision, error)
}
