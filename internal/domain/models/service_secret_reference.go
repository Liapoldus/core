package models

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	ServiceSecretReferenceActive        = "active"
	ServiceSecretReferenceRevoked       = "revoked"
	ServiceSecretReferenceLabelMaxRunes = 128
)

// ServiceSecretReference contains only safe catalog metadata. Secret bytes,
// storage locators and grant handles are deliberately not representable here.
type ServiceSecretReference struct {
	ID        string `json:"reference"`
	ServiceID string `json:"serviceId"`
	Purpose   string `json:"purpose"`
	Label     string `json:"label"`
	State     string `json:"state"`
	CreatedAt string `json:"createdAt"`
}

func (reference ServiceSecretReference) Validate() error {
	if !ValidServiceSecretReferenceID(reference.ID) || !validPeerLinkIdentifier(reference.ServiceID) ||
		!validServiceSecretPurpose(reference.Purpose) || !validServiceSecretLabel(reference.Label) ||
		(reference.State != ServiceSecretReferenceActive && reference.State != ServiceSecretReferenceRevoked) {
		return ServiceSecretReferenceInvalid{}
	}
	return nil
}

func ValidServiceSecretReferenceID(value string) bool {
	const prefix = "ssr:v1:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+32 {
		return false
	}
	for _, character := range value[len(prefix):] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func validServiceSecretPurpose(value string) bool {
	if value == "" || len(value) > 192 || strings.TrimSpace(value) != value {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) < 1 || len(parts) > 8 {
		return false
	}
	for _, part := range parts {
		if part == "" || part[0] < 'a' || part[0] > 'z' {
			return false
		}
		for _, character := range part {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return false
			}
		}
	}
	return true
}

func validServiceSecretLabel(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || utf8.RuneCountInString(value) > ServiceSecretReferenceLabelMaxRunes {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
