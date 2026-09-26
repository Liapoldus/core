package models

type PluginCookiePolicyValidationError struct {
	Message string
}

func (failure PluginCookiePolicyValidationError) Error() string { return failure.Message }
