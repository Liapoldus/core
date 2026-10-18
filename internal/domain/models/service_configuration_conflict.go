package models

type ServiceConfigurationConflict struct{ Reason string }

func (conflict ServiceConfigurationConflict) Error() string {
	switch ConfigurationBlocker(conflict.Reason) {
	case ConfigurationBlockerPending, ConfigurationBlockerServiceRollout, ConfigurationBlockerTrafficRollout, ConfigurationBlockerRevisionChanged:
		return "service configuration conflict: " + conflict.Reason
	default:
		return "service configuration revision conflict"
	}
}
