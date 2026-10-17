package interfaces

import (
	"crypto/x509"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ServiceReplicaIdentityResolver maps a verified service replica client
// certificate to the logical identity of its active replica lease. The HTTP
// adapter adds the certificate fingerprint before using the identity for a
// scoped secret grant.
type ServiceReplicaIdentityResolver func(*x509.Certificate) (models.ServiceReplicaIdentity, bool)
