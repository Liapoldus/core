package models

import (
	"encoding/hex"
	"time"
)

// PluginRolloutTarget is one immutable replica incarnation selected for a
// configuration rollout. A new incarnation with the same ReplicaID is a
// different target and cannot acknowledge this record.
type PluginRolloutTarget struct {
	ReplicaID      string
	IncarnationID  string
	ReleaseSHA256  string
	LeaseExpiresAt time.Time
	Acknowledged   bool
}

func (target PluginRolloutTarget) Valid() bool {
	if target.ReplicaID == "" || target.IncarnationID == "" || target.LeaseExpiresAt.IsZero() || len(target.ReleaseSHA256) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(target.ReleaseSHA256)
	if err != nil || len(decoded) != 32 {
		return false
	}
	for _, value := range target.ReleaseSHA256 {
		if value >= 'A' && value <= 'F' {
			return false
		}
	}
	return true
}
