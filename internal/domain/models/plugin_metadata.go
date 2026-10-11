package models

type PluginMetadata struct {
	Manifest            []byte
	ConfigurationSchema []byte
	Identity            any
	Readiness           any
}
