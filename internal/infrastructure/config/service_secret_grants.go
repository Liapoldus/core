package config

type ServiceSecretGrantPolicy struct {
	SchemaVersion      int `yaml:"schemaVersion"`
	MaximumOutstanding int `yaml:"maximumOutstanding"`
}

func LoadServiceSecretGrantPolicy() (ServiceSecretGrantPolicy, error) {
	return grantDefinitions(), nil
}
