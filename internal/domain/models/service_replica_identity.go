package models

// ServiceReplicaIdentity is the authenticated identity of one certificate on
// the private Service SDK listener. Fingerprint binds ephemeral credentials to
// the exact verified certificate, not merely its issuer or subject label.
type ServiceReplicaIdentity struct {
	InstanceID  string
	Fingerprint string
}
