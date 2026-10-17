package models

// OperationReplicaOutcome is the safe, durable result for one replica in a
// configuration rollout. It intentionally excludes endpoints and payloads.
type OperationReplicaOutcome struct {
	ReplicaID         string `json:"replicaId"`
	DesiredGeneration int64  `json:"desiredGeneration"`
	State             string `json:"state"`
	FailureCode       string `json:"failureCode,omitempty"`
}
