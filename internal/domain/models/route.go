package models

type Route struct {
	When     PathMatcher
	Site     string
	Proxy    *ProxyTarget
	Redirect *RouteRedirect
	Rewrite  *Rewrite
	Headers  *HeaderActions
}
