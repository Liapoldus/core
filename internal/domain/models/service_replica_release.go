package models

// ServiceReplicaRelease is the authenticated Core-side release claim for one
// currently leased replica. It is metadata, not executable-byte attestation.
type ServiceReplicaRelease struct {
	ReplicaID       string
	ArtifactVersion string
	ArtifactDigest  string
	Identity        any
	Readiness       any
	Configuration   ServiceReplicaConfigurationStatus
}
