package models

type CompiledGraph struct {
	Revision      Revision
	Listeners     []Listener
	Sites         map[string]Site
	Secrets       map[string]Secret
	Upstreams     map[string]Upstream
	TLSProfiles   map[string]TLSProfile
	Management    Management
	Observability Observability
}
