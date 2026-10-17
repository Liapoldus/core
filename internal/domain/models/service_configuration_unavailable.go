package models

type ServiceConfigurationUnavailable struct {
	Message string
}

func (failure ServiceConfigurationUnavailable) Error() string {
	return failure.Message
}
