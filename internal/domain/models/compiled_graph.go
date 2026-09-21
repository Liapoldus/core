package models

type CompiledGraph struct {
	Revision  Revision
	Listeners []Listener
	Sites     map[string]Site
}