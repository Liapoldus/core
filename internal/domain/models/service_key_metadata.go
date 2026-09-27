package models

type ServiceKeyMetadata struct {
	ID        string
	Name      string
	Role      string
	CreatedAt string
	ExpiresAt *string
	RevokedAt *string
}
