package models

type TrafficRolloutStage struct {
	ID                        string `json:"id"`
	CandidateWeightPercent    int    `json:"candidateWeightPercent"`
	MinimumObservationSeconds int    `json:"minimumObservationSeconds"`
	RequireManualApproval     bool   `json:"requireManualApproval"`
}
