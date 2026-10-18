package models

type ConfigurationBlocker string

const (
	ConfigurationBlockerPending         ConfigurationBlocker = "pending_configuration"
	ConfigurationBlockerServiceRollout  ConfigurationBlocker = "configuration_rollout_open"
	ConfigurationBlockerTrafficRollout  ConfigurationBlocker = "traffic_rollout_incomplete"
	ConfigurationBlockerRevisionChanged ConfigurationBlocker = "revision_changed"
)
