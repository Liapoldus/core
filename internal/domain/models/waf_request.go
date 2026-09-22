package models

type WAFRequest struct {
	Path          string
	Method        string
	RemoteAddress string
	RequestSize   uint64
	Headers       map[string][]string
	Query         map[string][]string
}
