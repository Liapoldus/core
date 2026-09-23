package models

type CompiledGraph struct {
	Revision         Revision
	RegistryRoot     string
	Listeners        []Listener
	Sites            map[string]Site
	Secrets          map[string]Secret
	Upstreams        map[string]Upstream
	TLSProfiles      map[string]TLSProfile
	RateLimits       map[string]RateLimit
	WAFPolicies      map[string]WAFPolicy
	DataProviders    map[string]DataProvider
	AuthPolicies     map[string]AuthPolicy
	Plugins          map[string]PluginInstance
	Management       Management
	Observability    Observability
}
