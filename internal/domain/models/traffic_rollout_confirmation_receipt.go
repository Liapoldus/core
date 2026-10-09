package models

type TrafficRolloutConfirmationReceipt struct {
	RolloutID          string `json:"rolloutId"`
	Revision           int64  `json:"revision"`
	StageID            string `json:"stageId"`
	AppliedWeight      int    `json:"appliedCandidateWeightPercent"`
	ControllerRevision string `json:"controllerRevision"`
	State              string `json:"state"`
}
