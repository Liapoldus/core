package models

type ServiceSecretReferenceNotFound struct{}

func (ServiceSecretReferenceNotFound) Error() string {
	return "service secret reference not found"
}
