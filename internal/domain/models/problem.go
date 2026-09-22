package models

import "fmt"

// Problem is an RFC 9457 problem details document. Values are bound from the
// versioned error catalog at the adapter layer; the domain never owns the
// catalog spelling.
type Problem struct {
	Type      string
	Title     string
	Status    int
	Code      string
	Detail    string
	Instance  string
	Path      string
	RequestID string
	CLIExit   int
}

func (problem Problem) Error() string {
	return fmt.Sprintf("%s (%d): %s", problem.Code, problem.Status, problem.Detail)
}
