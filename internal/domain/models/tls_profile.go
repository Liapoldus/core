package models

type TLSProfile struct {
	Certificates []TLSCertificate
	Protocols    []string
	ClientAuth   ClientAuth
}
