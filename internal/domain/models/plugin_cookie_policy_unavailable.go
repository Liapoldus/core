package models

type PluginCookiePolicyUnavailable struct {
	Message string
}

func (failure PluginCookiePolicyUnavailable) Error() string { return failure.Message }
