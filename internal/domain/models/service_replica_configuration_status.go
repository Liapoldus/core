package models

import "time"

type ServiceReplicaConfigurationStatus struct {
	DesiredGeneration *int64     `json:"desiredGeneration"`
	AppliedGeneration *int64     `json:"appliedGeneration"`
	State             string     `json:"state"`
	LastFailureCode   string     `json:"lastFailureCode,omitempty"`
	ObservedAt        *time.Time `json:"observedAt,omitempty"`
}

const (
	ServiceConfigurationUnconfigured = "unconfigured"
	ServiceConfigurationUnobserved   = "unobserved"
	ServiceConfigurationPending      = "pending"
	ServiceConfigurationAcknowledged = "acknowledged"
	ServiceConfigurationStale        = "stale"
	ServiceConfigurationFailed       = "failed"
	ServiceConfigurationUnreachable  = "unreachable"
)
