package models

// AuthPolicy binds a route policy name to an identity-plugin capability.
// Authentication remains outside Gateway; this is only the typed adapter
// boundary and its declarative configuration.
type AuthPolicy struct {
	Instance   string
	Capability string
}
