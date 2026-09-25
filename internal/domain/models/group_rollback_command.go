package models

import "time"

type GroupRollbackCommand struct {
	GroupID                 string
	Actor                   string
	RequestID               string
	IdempotencyKey          string
	IdempotencyScope        string
	ExpectedCurrentRevision *string
	IdempotencyWindow       time.Duration
	Now                     time.Time
}
