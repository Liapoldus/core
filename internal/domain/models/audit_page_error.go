package models

type AuditPageError struct {
	Message string
}

func (err AuditPageError) Error() string { return err.Message }
