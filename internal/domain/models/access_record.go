package models

import "time"

type AccessRecord struct {
	Timestamp time.Time
	RequestID string
	Listener  string
	Route     string
	Method    string
	Host      string
	Path      string
	Status    int
	Duration  time.Duration
	Bytes     int64
}
