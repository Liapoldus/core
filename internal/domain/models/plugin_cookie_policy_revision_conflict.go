package models

type PluginCookiePolicyRevisionConflict struct {
	Message string
}

func (failure PluginCookiePolicyRevisionConflict) Error() string { return failure.Message }
