// Package tls defines certificate and client identity ports.
package tls

type Profile struct{}

type CertificateProvider interface {
	Certificate(Profile) (any, error)
}
