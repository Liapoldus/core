package models

type PluginRegistration struct {
	ArtifactID      string `json:"artifactId"`
	ArtifactVersion string `json:"artifactVersion"`
	Digest          string `json:"digest"`
	InstanceID      string `json:"instanceId"`
	ReplicaID       string `json:"replicaId"`
	ControlEndpoint string `json:"controlEndpoint"`
	PeerEndpoint    string `json:"peerEndpoint,omitempty"`
	RuntimeStatus   string `json:"runtimeStatus"`
}
