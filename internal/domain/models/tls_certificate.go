package models

type TLSCertificate struct {
	Domains []string
	Issuer  string
	Cert    string
	Key     string
}
