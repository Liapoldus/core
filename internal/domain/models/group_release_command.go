package models

import "time"

type GroupReleaseCommand struct {
	GroupID                 string
	Actor                   string
	RequestID               string
	IdempotencyKey          string
	IdempotencyScope        string
	ExpectedCurrentRevision *string
	IdempotencyWindow       time.Duration
	Caddyfile               []byte
	Now                     time.Time
}
