package models

// ServiceSecretReferenceInvalid does not include any supplied metadata.
type ServiceSecretReferenceInvalid struct{}

func (ServiceSecretReferenceInvalid) Error() string {
	return "invalid service secret reference metadata"
}
