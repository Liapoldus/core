package interfaces

import "crypto/x509"

// ServiceReplicaIdentityResolver maps a verified service replica client
// certificate to the service instance whose configuration generation it may pull.
// It lives in the domain interfaces layer because both the configuration pull
// endpoint and the CLI composition root that serves it must name this port.
type ServiceReplicaIdentityResolver func(*x509.Certificate) (string, bool)
