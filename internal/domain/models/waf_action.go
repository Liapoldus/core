package models

type WAFAction struct {
	Allow bool
	Deny  *Deny
}
