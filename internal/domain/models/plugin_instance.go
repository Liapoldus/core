package models

import "time"

type PluginInstance struct {
	Binary                 string
	Args                   []string
	Env                    []string
	Capabilities           []string
	SecretGrants           []PluginSecretGrant
	Settings               []byte
	Timeout                time.Duration
	MemoryLimitBytes       uint64
	MemoryProbeInterval    time.Duration
	MaxConcurrentCalls     int
	RestartEnabled         bool
	RestartInitialBackoff  time.Duration
	RestartMaximumBackoff  time.Duration
	HealthProbeInterval    time.Duration
	HealthFailureThreshold int
}
