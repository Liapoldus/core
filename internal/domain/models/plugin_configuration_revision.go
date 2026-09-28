package models

import "time"

type PluginConfigurationRevision struct {
	InstanceID    string
	Revision      int64
	SchemaVersion int64
	Digest        string
	SettingsJSON  []byte
	State         string
	Actor         string
	CreatedAt     time.Time
}
