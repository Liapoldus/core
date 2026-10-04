package plugins

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"

	sdkinfra "github.com/Liapoldus/plugin-sdk/infrastructure"
)

var errPluginPeerRevoked = errors.New("plugin peer identity was refused")

// NewTLSRevocationVerifier adapts the Plugin SDK's signed CRL verifier to a
// standard-library TLS callback. An empty file list disables CRL checks; once
// configured, unavailable, invalid, expired and revoked identities fail closed.
func NewTLSRevocationVerifier(authorityPath string, files []string) (func(tls.ConnectionState) error, error) {
	if len(files) == 0 {
		return func(tls.ConnectionState) error { return nil }, nil
	}
	contents, err := os.ReadFile(authorityPath)
	if err != nil {
		return nil, errPluginPeerRevoked
	}
	defer clear(contents)
	authorities := make([]*x509.Certificate, 0, 2)
	for len(contents) > 0 {
		block, remaining := pem.Decode(contents)
		contents = remaining
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, parseErr := x509.ParseCertificate(block.Bytes)
		if parseErr != nil || !certificate.IsCA {
			return nil, errPluginPeerRevoked
		}
		authorities = append(authorities, certificate)
	}
	if len(authorities) == 0 {
		return nil, errPluginPeerRevoked
	}
	revocation, err := sdkinfra.NewRevocation(sdkinfra.RevocationConfiguration{Authorities: authorities, Files: files})
	if err != nil {
		return nil, errPluginPeerRevoked
	}
	return func(state tls.ConnectionState) error {
		if !revocation.Available() || len(state.VerifiedChains) == 0 || len(state.VerifiedChains[0]) == 0 {
			return errPluginPeerRevoked
		}
		for _, certificate := range state.VerifiedChains[0] {
			revoked, checkErr := revocation.Revoked(certificate.SerialNumber.Bytes())
			if checkErr != nil || revoked {
				return errPluginPeerRevoked
			}
		}
		return nil
	}, nil
}
