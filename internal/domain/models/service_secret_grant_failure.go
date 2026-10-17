package models

// ServiceSecretGrantFailure never includes a handle, reference, path, or value.
type ServiceSecretGrantFailure struct {
	Kind ServiceSecretGrantFailureKind
}

func (ServiceSecretGrantFailure) Error() string { return "service secret grant request failed" }
