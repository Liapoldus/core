package models

type PluginConfigurationConflict struct{}

func (PluginConfigurationConflict) Error() string {
	return "plugin configuration revision conflict"
}
