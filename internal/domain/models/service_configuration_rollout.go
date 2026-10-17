package models

// ServiceConfigurationRollout identifies a durable generation delivery barrier.
type ServiceConfigurationRollout struct {
	OperationID string
	InstanceID  string
	Generation  int64
}
