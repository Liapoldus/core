package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type ConfigCompiler interface {
	Compile(path string) (models.CompiledGraph, error)
}
