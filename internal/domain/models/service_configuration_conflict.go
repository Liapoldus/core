package models

type ServiceConfigurationConflict struct{}

func (ServiceConfigurationConflict) Error() string {
	return "service configuration revision conflict"
}
