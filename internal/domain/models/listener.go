package models

type Listener struct {
	Address string
	IsHTTP  bool
	Routes  []Route
}