package models

// PluginSecretGrantFailure never includes a handle, reference, path, or value.
type PluginSecretGrantFailure struct {
	Kind PluginSecretGrantFailureKind
}

func (PluginSecretGrantFailure) Error() string { return "plugin secret grant request failed" }
