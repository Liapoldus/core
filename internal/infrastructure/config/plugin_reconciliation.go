package config

import (
	"time"
)

type PluginReconciliationPolicy struct {
	SchemaVersion       int `yaml:"schemaVersion"`
	ReadinessPollMillis int `yaml:"readinessPollMillis"`
}

func LoadPluginReconciliationPolicy() (PluginReconciliationPolicy, error) {
	return reconciliationDefinitions(), nil
}

func (policy PluginReconciliationPolicy) ReadinessPollInterval() time.Duration {
	return time.Duration(policy.ReadinessPollMillis) * time.Millisecond
}
