package models

import "time"

type AuditRecord struct {
	Timestamp    time.Time
	Actor        string
	Action       string
	Resource     string
	Result       string
	RequestID    string
	DigestBefore string
	DigestAfter  string
}
