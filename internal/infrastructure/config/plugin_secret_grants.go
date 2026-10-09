package config

type PluginSecretGrantPolicy struct {
	SchemaVersion      int `yaml:"schemaVersion"`
	MaximumOutstanding int `yaml:"maximumOutstanding"`
}

func LoadPluginSecretGrantPolicy() (PluginSecretGrantPolicy, error) {
	return grantDefinitions(), nil
}
