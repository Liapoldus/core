package models

type PluginConfigurationUnavailable struct {
	Message string
}

func (failure PluginConfigurationUnavailable) Error() string {
	return failure.Message
}
