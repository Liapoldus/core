package models

import "fmt"

// Problem is an RFC 9457 problem details document. Values are bound from the
// versioned error catalog at the adapter layer; the domain never owns the
// catalog spelling.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Instance  string `json:"instance"`
	Path      string `json:"path,omitempty"`
	RequestID string `json:"requestId"`
	CLIExit   int    `json:"-"`
}

func (problem Problem) Error() string {
	return fmt.Sprintf("%s (%d): %s", problem.Code, problem.Status, problem.Detail)
}
