package models

type AuditPage struct {
	Items      []AuditRecord
	NextCursor *string
}
