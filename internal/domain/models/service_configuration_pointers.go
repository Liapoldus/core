package models

type ServiceConfigurationPointers struct {
	InstanceID       string
	CurrentRevision  int64
	PreviousRevision int64
	PendingRevision  int64
}
