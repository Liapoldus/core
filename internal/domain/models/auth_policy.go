package models

// AuthPolicy binds a route policy name to a declared plugin capability.
type AuthPolicy struct {
	Instance       string
	Capability     string
	ContextSecrets []string
}
