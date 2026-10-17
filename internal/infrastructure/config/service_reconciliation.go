package config

import (
	"time"
)

type ServiceReconciliationPolicy struct {
	SchemaVersion       int `yaml:"schemaVersion"`
	ReadinessPollMillis int `yaml:"readinessPollMillis"`
}

func LoadServiceReconciliationPolicy() (ServiceReconciliationPolicy, error) {
	return reconciliationDefinitions(), nil
}

func (policy ServiceReconciliationPolicy) ReadinessPollInterval() time.Duration {
	return time.Duration(policy.ReadinessPollMillis) * time.Millisecond
}
