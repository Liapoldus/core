package models

import "time"

type TrafficRolloutStageRecord struct {
	Index                  int
	Stage                  TrafficRolloutStage
	State                  string
	StartedAt              time.Time
	ConfirmedAt            time.Time
	AppliedCandidateWeight int
	ControllerRevision     string
	ApprovedBy             string
	ApprovedAt             time.Time
}
