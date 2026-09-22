package models

type RouteCORS struct {
	Origins       []string
	Methods       []string
	Headers       []string
	ExposeHeaders []string
	Credentials   bool
	MaxAge        string
}
