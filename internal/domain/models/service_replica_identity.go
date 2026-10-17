package models

// ServiceReplicaIdentity is the logical identity resolved from an active
// replica lease. Fingerprint, when present, binds ephemeral credentials to the
// exact verified certificate rather than only its issuer or subject label.
type ServiceReplicaIdentity struct {
	InstanceID    string
	ReplicaID     string
	IncarnationID string
	PlacementID   string
	Fingerprint   string
}
