package models

type TLSCertificate struct {
	Domains []string
	Cert    string
	Key     string
}
