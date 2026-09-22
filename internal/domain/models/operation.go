package models

import "time"

type Operation struct {
	ID         string
	State      string
	CreatedAt  time.Time
	StartedAt  time.Time
	FinishedAt time.Time
	Result     any
	Problem    *Problem
}
