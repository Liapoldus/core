package models

type Listener struct {
	Address    string
	Type       string
	IsHTTP     bool
	TLSProfile string
	Routes     []Route
	Rules      []Route
}
