package models

type Listener struct {
	Address    string
	Type       string
	IsHTTP     bool
	TLSProfile string
	Limits     ListenerLimits
	Routes     []Route
	Rules      []Route
}
