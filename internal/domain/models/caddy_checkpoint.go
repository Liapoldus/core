package models

import "time"

type CaddyCheckpoint struct {
	ID             string
	RuntimeDigest  string
	SnapshotPath   string
	OperationID    string
	OperationState string
	Actor          string
	CreatedAt      time.Time
}
