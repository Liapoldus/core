package models

import "time"

type GroupReleaseReservation struct {
	GroupID                 string
	ExpectedCurrentRevision *string
	Actor                   string
	Scope                   string
	KeyDigest               string
	RequestDigest           string
	OperationID             string
	RevisionID              string
	OperationKind           string
	OperationState          string
	RequestID               string
	CaddyfileDigest         string
	ArtifactDigest          *string
	CaddyfilePath           string
	ArtifactPath            *string
	CreatedAt               time.Time
	ExpiresAt               time.Time
}
