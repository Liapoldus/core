package bootstrap

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/config"
)

func ManagementTLS(bootstrap config.BootstrapConfig, invalidConfiguration string) (*tls.Config, error) {
	certificatePEM, err := os.ReadFile(bootstrap.ManagementCertificate)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(bootstrap.ManagementKey)
	if err != nil {
		return nil, err
	}
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return nil, err
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	if bootstrap.ManagementClientCA != "" {
		caPEM, err := os.ReadFile(bootstrap.ManagementClientCA)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, errors.New(invalidConfiguration)
		}
		result.ClientCAs = roots
		result.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return result, nil
}
