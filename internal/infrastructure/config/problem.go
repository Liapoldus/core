package config

import (
	"errors"

	"github.com/Liapoldus/core/internal/domain/models"
)

// CompileProblem carries a typed RFC 9457 problem for config and registry
// compile failures so adapters can surface a specific error code instead of a
// generic config_invalid.
type CompileProblem struct {
	Problem models.Problem
	Cause   error
}

func (problem *CompileProblem) Error() string { return problem.Problem.Error() }

func (problem *CompileProblem) Unwrap() error { return problem.Cause }

// ProblemFrom extracts the typed problem carried by a compile failure.
func ProblemFrom(err error) (models.Problem, bool) {
	var compileProblem *CompileProblem
	if errors.As(err, &compileProblem) {
		return compileProblem.Problem, true
	}
	return models.Problem{}, false
}
