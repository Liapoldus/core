package config

import (
	"bytes"
	"io"
	"time"

	"github.com/Liapoldus/core"
	"gopkg.in/yaml.v3"
)

type PluginReconciliationPolicy struct {
	SchemaVersion       int `yaml:"schemaVersion"`
	ReadinessPollMillis int `yaml:"readinessPollMillis"`
}

func LoadPluginReconciliationPolicy() (PluginReconciliationPolicy, error) {
	contents, err := core.Contract(core.PluginReconciliation)
	if err != nil {
		return PluginReconciliationPolicy{}, ErrInvalidDocument
	}
	var policy PluginReconciliationPolicy
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&policy); err != nil {
		return PluginReconciliationPolicy{}, ErrInvalidDocument
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || policy.SchemaVersion != 1 || policy.ReadinessPollMillis <= 0 {
		return PluginReconciliationPolicy{}, ErrInvalidDocument
	}
	return policy, nil
}

func (policy PluginReconciliationPolicy) ReadinessPollInterval() time.Duration {
	return time.Duration(policy.ReadinessPollMillis) * time.Millisecond
}
