package models

import "time"

type GroupReleaseCommit struct {
	GroupID                 string
	ExpectedCurrentRevision *string
	Revision                GroupRevision
	OperationID             string
	OperationState          string
	JournalState            string
	UpdatedAt               time.Time
	Audit                   AuditRecord
}
