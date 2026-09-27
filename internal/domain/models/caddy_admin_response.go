package models

type CaddyAdminResponse struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}
