package models

type PluginConfigurationRejected struct{}

func (PluginConfigurationRejected) Error() string {
	return "plugin rejected the configuration"
}
