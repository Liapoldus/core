package models

type ServiceSecretReferenceConflict struct{}

func (ServiceSecretReferenceConflict) Error() string {
	return "service secret reference already exists"
}
