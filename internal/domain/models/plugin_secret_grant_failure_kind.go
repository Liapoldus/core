package models

// PluginSecretGrantFailureKind is an internal classification mapped to the
// Plugin SDK outcome contract at the presentation boundary.
type PluginSecretGrantFailureKind uint8

const (
	PluginSecretGrantDenied PluginSecretGrantFailureKind = iota + 1
	PluginSecretGrantUnknown
	PluginSecretGrantExpired
	PluginSecretGrantSpent
	PluginSecretGrantUnavailable
)
