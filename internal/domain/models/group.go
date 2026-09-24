package models

type Group struct {
	ID                 string
	Kind               string
	Active             bool
	CreatedAt          string
	ArchivedAt         *string
	CurrentRevisionID  *string
	PreviousRevisionID *string
}
