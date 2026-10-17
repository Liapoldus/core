package models

type ServiceSecretReferenceUnavailable struct{}

func (ServiceSecretReferenceUnavailable) Error() string {
	return "service secret reference catalog unavailable"
}
