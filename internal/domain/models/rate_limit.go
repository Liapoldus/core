package models

type RateLimit struct {
	Key      string
	Requests int
	Per      string
	Burst    int
}
