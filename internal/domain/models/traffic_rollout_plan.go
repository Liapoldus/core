package models

// TrafficRolloutPlan is the product-neutral stage description stored by Core.
// It carries exact replica identities but no plugin-owned configuration fields.
type TrafficRolloutPlan struct {
	ReleaseSHA256 string                 `json:"releaseSha256"`
	Targets       []TrafficRolloutTarget `json:"targets"`
	Stages        []TrafficRolloutStage  `json:"stages"`
}
