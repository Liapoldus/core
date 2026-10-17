package models

import "time"

// ServiceSecretGrantReceipt is the public SDK grant descriptor. Secret bytes
// and replica identity are intentionally absent from this response model.
type ServiceSecretGrantReceipt struct {
	Handle     string    `json:"handle"`
	Reference  string    `json:"reference"`
	Purpose    string    `json:"purpose"`
	Generation string    `json:"generation"`
	ExpiresAt  time.Time `json:"expiresAt"`
}
