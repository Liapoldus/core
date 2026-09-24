package models

type GroupRevision struct {
	ID              string
	GroupID         string
	CaddyfileDigest string
	ArtifactDigest  *string
	CaddyfilePath   string
	ArtifactPath    *string
	CreatedAt       string
	Actor           string
}
