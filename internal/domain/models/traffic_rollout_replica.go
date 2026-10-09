package models

import "time"

type TrafficRolloutReplica struct {
	ReplicaID      string
	Incarnation    string
	ReleaseSHA256  string
	LeaseExpiresAt time.Time
	Ready          bool
}
