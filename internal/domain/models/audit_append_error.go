package models

type AuditAppendError struct {
	Message string
}

func (failure AuditAppendError) Error() string {
	return failure.Message
}
