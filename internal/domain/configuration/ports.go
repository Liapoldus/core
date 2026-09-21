// Package configuration defines configuration invariants and external ports.
package configuration

type Revision struct {
	Value  string
	Digest string
}

type CompiledGraph struct {
	Revision Revision
}

type Source interface {
	Read(path string) ([]byte, error)
}

type Compiler interface {
	Compile(path string) (CompiledGraph, error)
}
