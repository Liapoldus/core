// Package application orchestrates Gateway use cases.
package application

import (
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type ConfigurationService struct {
	Compiler interfaces.ConfigCompiler
}

func (service ConfigurationService) Compile(path string) (models.CompiledGraph, error) {
	return service.Compiler.Compile(path)
}
