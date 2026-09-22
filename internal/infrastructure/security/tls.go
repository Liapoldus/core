// Package security contains TLS and security-policy adapters.
package security

import (
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Liapoldus/core/internal/domain/models"
)

type CertificateMaterial struct {
	Certificate []byte
	PrivateKey  []byte
}

// TLSManager owns protected certificate storage. Replacements are atomic.
type TLSManager struct{ Root string }

func NewTLSManager(root string) (TLSManager, error) {
	if strings.TrimSpace(root) == "" {
		return TLSManager{}, errors.New("TLS storage root is required")
	}
	return TLSManager{Root: root}, nil
}

func (manager TLSManager) Store(issuer string, material CertificateMaterial) (models.TLSCertificate, error) {
	if strings.TrimSpace(issuer) == "" {
		return models.TLSCertificate{}, errors.New("TLS issuer is required")
	}
	if len(material.Certificate) == 0 || len(material.PrivateKey) == 0 {
		return models.TLSCertificate{}, errors.New("TLS certificate and private key are required")
	}
	if _, err := tls.X509KeyPair(material.Certificate, material.PrivateKey); err != nil {
		return models.TLSCertificate{}, fmt.Errorf("validate TLS certificate: %w", err)
	}
	directory := filepath.Join(manager.Root, issuer)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return models.TLSCertificate{}, fmt.Errorf("create TLS storage: %w", err)
	}
	certificatePath, keyPath := filepath.Join(directory, "certificate.pem"), filepath.Join(directory, "private-key.pem")
	if err := atomicWrite(certificatePath, material.Certificate, 0o600); err != nil {
		return models.TLSCertificate{}, err
	}
	if err := atomicWrite(keyPath, material.PrivateKey, 0o600); err != nil {
		return models.TLSCertificate{}, err
	}
	return models.TLSCertificate{Issuer: issuer, Cert: certificatePath, Key: keyPath}, nil
}

func (manager TLSManager) Remove(issuer string) error {
	if strings.TrimSpace(issuer) == "" {
		return errors.New("TLS issuer is required")
	}
	if err := os.RemoveAll(filepath.Join(manager.Root, issuer)); err != nil {
		return fmt.Errorf("remove TLS material: %w", err)
	}
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tls-*")
	if err != nil {
		return fmt.Errorf("create TLS temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace TLS material: %w", err)
	}
	return nil
}
