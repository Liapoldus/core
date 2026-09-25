package models

import "time"

type Operation struct {
	ID        string
	Kind      string
	State     string
	CreatedAt time.Time
	UpdatedAt *time.Time
	RequestID string
	Actor     string
	Resource  string
}
