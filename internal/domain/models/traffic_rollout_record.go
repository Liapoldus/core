package models

import "time"

type TrafficRolloutRecord struct {
	ID                 string
	OperationID        string
	InstanceID         string
	Generation         int64
	ReleaseSHA256      string
	PlanJSON           []byte
	State              string
	ActiveStageIndex   int
	ControllerRevision string
	Revision           int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CompletedAt        time.Time
	Stages             []TrafficRolloutStageRecord
	Targets            []TrafficRolloutCohortTarget
}
