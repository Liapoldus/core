package models

type PluginCookiePolicyNotFound struct {
	Message string
}

func (failure PluginCookiePolicyNotFound) Error() string { return failure.Message }
