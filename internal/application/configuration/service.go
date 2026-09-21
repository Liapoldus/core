// Package configuration orchestrates configuration compilation and activation.
package configuration

import "github.com/Liapoldus/core/internal/domain/configuration"

type Service struct {
	Compiler configuration.Compiler
}

func (service Service) Compile(path string) (configuration.CompiledGraph, error) {
	return service.Compiler.Compile(path)
}
