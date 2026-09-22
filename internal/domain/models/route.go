package models

type Route struct {
	When      PathMatcher
	Site      string
	Proxy     *ProxyTarget
	Redirect  *RouteRedirect
	Rewrite   *Rewrite
	Headers   *HeaderActions
	Deny      *Deny
	Plugin    *PluginTarget
	Auth      string
	WAF       string
	RateLimit string
	CORS      *RouteCORS
	Cache     *RouteCache
}
