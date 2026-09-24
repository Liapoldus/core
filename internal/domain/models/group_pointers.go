package models

type GroupPointers struct {
	GroupID            string
	CurrentRevisionID  *string
	PreviousRevisionID *string
	UpdatedAt          string
}
