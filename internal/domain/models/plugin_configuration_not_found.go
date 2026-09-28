package models

type PluginConfigurationNotFound struct{}

func (PluginConfigurationNotFound) Error() string {
	return "plugin configuration revision not found"
}
