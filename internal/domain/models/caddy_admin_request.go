package models

type CaddyAdminRequest struct {
	Method  string
	Path    string
	Headers map[string][]string
	Body    []byte
}
