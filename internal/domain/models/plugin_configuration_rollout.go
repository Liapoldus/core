package models

// PluginConfigurationRollout identifies a durable generation delivery barrier.
type PluginConfigurationRollout struct {
	OperationID string
	InstanceID  string
	Generation  int64
}
