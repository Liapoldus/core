package models

type TrafficRolloutConflict struct{}

func (TrafficRolloutConflict) Error() string { return "traffic rollout conflict" }
