package models

type TrafficRolloutInvalid struct{}

func (TrafficRolloutInvalid) Error() string { return "invalid traffic rollout" }
