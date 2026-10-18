package models

// ServiceConfigurationCandidateValidationFailed reports a safe failure while
// validating a staged service configuration candidate. Product and transport
// details remain private to the service boundary.
type ServiceConfigurationCandidateValidationFailed struct{}

func (ServiceConfigurationCandidateValidationFailed) Error() string {
	return "service configuration candidate validation failed"
}
