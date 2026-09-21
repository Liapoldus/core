// Package application orchestrates Gateway use cases.
package application

import "github.com/Liapoldus/core/internal/domain/configuration"

type ConfigurationService struct {
	Compiler configuration.Compiler
}

func (service ConfigurationService) Compile(path string) (configuration.CompiledGraph, error) {
	return service.Compiler.Compile(path)
}
