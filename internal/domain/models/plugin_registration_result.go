package models

type PluginRegistrationResult struct {
	Accepted   bool   `json:"accepted"`
	InstanceID string `json:"instanceId"`
	ReplicaID  string `json:"replicaId"`
	Status     string `json:"status"`
}
