package models

import "time"

type TrafficRolloutCohortTarget struct {
	Cohort         string
	ReplicaID      string
	IncarnationID  string
	ReleaseSHA256  string
	LeaseExpiresAt time.Time
}
