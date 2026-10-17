package models

type ServiceMetadata struct {
	Manifest            []byte
	ConfigurationSchema []byte
	Identity            any
	Readiness           any
	ReplicaID           string
	ArtifactVersion     string
	ArtifactDigest      string
	Replicas            []ServiceReplicaRelease
}
