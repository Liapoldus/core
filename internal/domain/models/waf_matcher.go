package models

type WAFMatcher struct {
	Path     PathMatcher
	Method   *StringMatcher
	SourceIP *IPMatcher
}

func (matcher WAFMatcher) Matches(path, method, remoteAddress string) bool {
	if !matcher.Path.Matches(path) {
		return false
	}
	if matcher.Method != nil && !matcher.Method.Matches(method, true) {
		return false
	}
	if matcher.SourceIP != nil && !matcher.SourceIP.Matches(remoteAddress) {
		return false
	}
	return true
}
