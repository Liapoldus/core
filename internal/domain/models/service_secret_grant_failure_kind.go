package models

// ServiceSecretGrantFailureKind is an internal classification mapped to the
// Service SDK outcome contract at the presentation boundary.
type ServiceSecretGrantFailureKind uint8

const (
	ServiceSecretGrantDenied ServiceSecretGrantFailureKind = iota + 1
	ServiceSecretGrantUnknown
	ServiceSecretGrantExpired
	ServiceSecretGrantSpent
	ServiceSecretGrantUnavailable
)
