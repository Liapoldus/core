package config

import (
	"bytes"
	"io"

	"github.com/Liapoldus/core"
	"gopkg.in/yaml.v3"
)

type PluginSecretGrantPolicy struct {
	SchemaVersion      int `yaml:"schemaVersion"`
	MaximumOutstanding int `yaml:"maximumOutstanding"`
}

func LoadPluginSecretGrantPolicy() (PluginSecretGrantPolicy, error) {
	contents, err := core.Contract(core.PluginSecretGrants)
	if err != nil {
		return PluginSecretGrantPolicy{}, err
	}
	var policy PluginSecretGrantPolicy
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&policy); err != nil {
		return PluginSecretGrantPolicy{}, ErrInvalidDocument
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return PluginSecretGrantPolicy{}, ErrInvalidDocument
	}
	if policy.SchemaVersion != 1 || policy.MaximumOutstanding < 1 {
		return PluginSecretGrantPolicy{}, ErrInvalidDocument
	}
	return policy, nil
}
