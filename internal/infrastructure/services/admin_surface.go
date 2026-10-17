package services

import (
	"errors"
	"fmt"
	"regexp"
)

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type AdminSurface struct {
	Service      string
	NamespaceID  string
	Version      string
	Title        string
	Capabilities []string
}

func (s AdminSurface) Namespace() string { return s.NamespaceID }
func ValidateAdminSurface(surface AdminSurface) error {
	if surface.Service == "" {
		return errors.New("admin surface service is required")
	}
	if !namespacePattern.MatchString(surface.NamespaceID) {
		return fmt.Errorf("invalid admin surface namespace: %s", surface.NamespaceID)
	}
	if surface.Version == "" {
		return errors.New("admin surface version is required")
	}
	if surface.Title == "" {
		return errors.New("admin surface title is required")
	}
	if len(surface.Capabilities) == 0 {
		return errors.New("admin surface capabilities are required")
	}
	return nil
}
