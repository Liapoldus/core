package models

type Release struct {
	ID         string
	PreviousID string `json:"-"`
}
