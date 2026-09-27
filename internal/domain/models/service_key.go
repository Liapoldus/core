package models

type ServiceKey struct {
	ID       string
	Name     string
	Verifier []byte
	Role     string
}
