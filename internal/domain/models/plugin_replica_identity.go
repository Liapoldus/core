package models

// PluginReplicaIdentity is the authenticated identity of one certificate on
// the private Plugin SDK listener. Fingerprint binds ephemeral credentials to
// the exact verified certificate, not merely its issuer or subject label.
type PluginReplicaIdentity struct {
	InstanceID  string
	Fingerprint string
}
