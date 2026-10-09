package models

// PluginConfigurationConvergencePending distinguishes a valid desired
// generation that still awaits replica acknowledgements from an invalid apply.
// It is internal control flow and must not be exposed as an error response.
type PluginConfigurationConvergencePending struct{}

func (PluginConfigurationConvergencePending) Error() string { return "" }
