package models

type ServiceConfigurationNotFound struct{}

func (ServiceConfigurationNotFound) Error() string {
	return "service configuration revision not found"
}
