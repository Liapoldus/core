package config

// BootstrapConfig is the typed runtime input assembled by the Core composition
// root from environment variables and persisted JSON settings. It is not a
// user-facing file format; project files and Git are owned by standalone CLI.
type BootstrapConfig struct {
	SourcePath               string
	StatePath                string
	ManagementListen         string
	ManagementCertificate    string
	ManagementKey            string
	ManagementClientCA       string
	ManagementMaxBodyBytes   int64
	ManagementHeaderTimeout  string
	ManagementRequestTimeout string
	PluginControlListen      string
	PluginControlPublicURL   string
	PluginControlCertificate string
	PluginControlKey         string
	PluginReplicaClientCA    string
	PluginReplicaServerCA    string
	PluginReplicaClientCRLs  []string
	PluginReplicaServerCRLs  []string
	Plugins                  []PluginInstanceConfig
}

// PluginReplicaConfig is one operator-declared plugin replica. Endpoint and
// expected identity are the normative registration values for the Core process
// lifetime: Core never derives, discovers or accepts them from a plugin.
type PluginReplicaConfig struct {
	ReplicaID            string
	Endpoint             string
	ExpectedPeerIdentity PeerIdentityConfig
}

// PluginInstanceConfig is one operator-declared plugin instance and the
// replicas that serve it.
type PluginInstanceConfig struct {
	InstanceID string
	Replicas   []PluginReplicaConfig
}

// PeerIdentityConfig is the expected mTLS identity of one replica, expressed
// as an exact common name plus an optional exact URI.
type PeerIdentityConfig struct {
	CommonName                string
	UniformResourceIdentifier string
}
