package models

type WAFAction struct {
	Allow   bool
	Deny    *Deny
	Limit   string
	Problem *Problem
}
