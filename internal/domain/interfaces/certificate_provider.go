package interfaces

import "github.com/Liapoldus/core/internal/domain/models"

type CertificateProvider interface {
	Certificate(models.TLSProfile) (models.TLSCertificate, error)
}
