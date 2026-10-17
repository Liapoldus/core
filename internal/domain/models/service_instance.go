package models

import "time"

type ServiceInstance struct {
	Binary           string
	Capabilities     []string
	SecretGrants     []ServiceSecretGrant
	Settings         []byte
	SettingsRevision string
	// ConfigGrantSecrets are temporary secret bytes, keyed by opaque references
	// already substituted into Settings; Runtime transfers and clears this map.
	ConfigGrantSecrets     map[string][]byte `json:"-"`
	ConfigGrantPurpose     string
	Timeout                time.Duration
	StartTimeout           time.Duration
	MemoryLimitBytes       uint64
	MemoryProbeInterval    time.Duration
	MaxConcurrentCalls     int
	RestartEnabled         bool
	RestartInitialBackoff  time.Duration
	RestartMaximumBackoff  time.Duration
	HealthProbeInterval    time.Duration
	HealthFailureThreshold int
}
