package models

type PluginConfigurationPointers struct {
	InstanceID       string
	CurrentRevision  int64
	PreviousRevision int64
	PendingRevision  int64
}
