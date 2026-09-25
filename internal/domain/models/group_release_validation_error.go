package models

type GroupReleaseValidationError struct {
	Cause error
}

func (validation GroupReleaseValidationError) Error() string {
	if validation.Cause == nil {
		return ""
	}
	return validation.Cause.Error()
}

func (validation GroupReleaseValidationError) Unwrap() error {
	return validation.Cause
}
