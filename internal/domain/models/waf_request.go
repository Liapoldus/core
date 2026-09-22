package models

type WAFRequest struct {
	Path          string
	Method        string
	RemoteAddress string
	Headers       map[string][]string
	Query         map[string][]string
}
