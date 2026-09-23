package models

type ReleaseRevisionConflict struct {
	ExpectedRevision *string
	CurrentRevision  *string
}
