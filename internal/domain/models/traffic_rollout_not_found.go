package models

type TrafficRolloutNotFound struct{}

func (TrafficRolloutNotFound) Error() string { return "traffic rollout not found" }
