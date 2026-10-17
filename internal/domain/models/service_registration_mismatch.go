package models

// ServiceRegistrationMismatch means CLI's artifact metadata does not match the
// independently authenticated Service SDK replica lease.
type ServiceRegistrationMismatch struct{}

func (ServiceRegistrationMismatch) Error() string {
	return "service registration does not match the running artifact"
}

var ErrServiceRegistrationMismatch error = ServiceRegistrationMismatch{}
