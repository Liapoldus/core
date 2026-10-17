package config

// BootstrapConfig is the typed runtime input assembled by the Core composition
// root from environment variables and persisted JSON settings. It is not a
// user-facing file format; project files and Git are owned by standalone CLI.
type BootstrapConfig struct {
	SecretRoot                string
	StatePath                 string
	ManagementListen          string
	ManagementCertificate     string
	ManagementKey             string
	ManagementClientCA        string
	ManagementMaxBodyBytes    int64
	ManagementHeaderTimeout   string
	ManagementRequestTimeout  string
	ServiceControlListen      string
	ServiceControlPublicURL   string
	ServiceControlCertificate string
	ServiceControlKey         string
	ServiceReplicaClientCA    string
	ServiceReplicaServerCA    string
	ServiceReplicaClientCRLs  []string
	ServiceReplicaServerCRLs  []string
	Services                  []ServiceInstanceConfig
}

// ServiceReplicaConfig is one operator-declared service replica. Endpoint and
// expected identity are the normative registration values for the Core process
// lifetime: Core never derives, discovers or accepts them from a service.
type ServiceReplicaConfig struct {
	ReplicaID            string
	Endpoint             string
	ExpectedPeerIdentity PeerIdentityConfig
}

// ServiceInstanceConfig is one operator-declared service instance and the
// replicas that serve it.
type ServiceInstanceConfig struct {
	InstanceID string
	Replicas   []ServiceReplicaConfig
}

// PeerIdentityConfig is the expected mTLS identity of one replica, expressed
// as an exact common name plus an optional exact URI.
type PeerIdentityConfig struct {
	CommonName                string
	UniformResourceIdentifier string
}
