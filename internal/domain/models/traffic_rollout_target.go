package models

type TrafficRolloutTarget struct {
	ReplicaID   string `json:"replicaId"`
	Incarnation string `json:"incarnation"`
}
