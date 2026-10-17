package models

type ServiceConfigurationRejected struct{}

func (ServiceConfigurationRejected) Error() string {
	return "service rejected the configuration"
}
