package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/plugin-sdk/domain/models"
)

func main() {
	expectedURI, _ := url.Parse("spiffe://liapoldus/plugin/forms/replica-a/incarnation-1")
	wrongURI, _ := url.Parse("spiffe://liapoldus/plugin/forms/replica-b/incarnation-1")
	uriIdentity, _ := models.NewPeerIdentity("", expectedURI.String())
	cnIdentity, _ := models.NewPeerIdentity("forms-replica", "")

	uriVerifier := plugins.PinnedReplicaIdentityVerifier(uriIdentity.CommonName, uriIdentity.UniformResourceIdentifier, nil)
	cnVerifier := plugins.PinnedReplicaIdentityVerifier(cnIdentity.CommonName, cnIdentity.UniformResourceIdentifier, nil)
	denyRevocation := plugins.PinnedReplicaIdentityVerifier(uriIdentity.CommonName, uriIdentity.UniformResourceIdentifier, func(tls.ConnectionState) error {
		return fmt.Errorf("revoked")
	})

	result := map[string]bool{
		"exactURIAccepted":           uriVerifier(connection("", expectedURI)) == nil,
		"wrongURIRejected":           uriVerifier(connection("", wrongURI)) != nil,
		"missingURIRejected":         uriVerifier(connection("", nil)) != nil,
		"exactCommonNameAccepted":    cnVerifier(connection("forms-replica", nil)) == nil,
		"revocationFailurePreserved": errorsIsDenied(denyRevocation(connection("", expectedURI))),
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}

func connection(commonName string, identifiers ...*url.URL) tls.ConnectionState {
	certificate := &x509.Certificate{}
	certificate.Subject.CommonName = commonName
	for _, identifier := range identifiers {
		if identifier != nil {
			certificate.URIs = append(certificate.URIs, identifier)
		}
	}
	return tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}
}

func errorsIsDenied(err error) bool { return err != nil }
