package models

import "strings"

type WAFMatcher struct {
	Path     PathMatcher
	Method   *StringMatcher
	SourceIP *IPMatcher
	Headers  map[string]StringMatcher
	Query    map[string]StringMatcher
}

func (matcher WAFMatcher) Matches(request WAFRequest) bool {
	if !matcher.Path.Matches(request.Path) {
		return false
	}
	if matcher.Method != nil && !matcher.Method.Matches(request.Method, true) {
		return false
	}
	if matcher.SourceIP != nil && !matcher.SourceIP.Matches(request.RemoteAddress) {
		return false
	}
	for name, expected := range matcher.Headers {
		var values []string
		exists := false
		for actual, items := range request.Headers {
			if strings.EqualFold(actual, name) {
				values, exists = items, true
				break
			}
		}
		if !expected.MatchesAny(values, exists) {
			return false
		}
	}
	for name, expected := range matcher.Query {
		values, exists := request.Query[name]
		if !expected.MatchesAny(values, exists) {
			return false
		}
	}
	return true
}
