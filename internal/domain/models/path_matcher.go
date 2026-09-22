package models

import (
	"regexp"
	"strings"
)

type PathMatcher struct {
	Prefixes []string
	Exact    string
	Regex    *regexp.Regexp
	Host     *StringMatcher
	Method   *StringMatcher
	Headers  map[string]StringMatcher
	Query    map[string]StringMatcher
}

func (matcher PathMatcher) Matches(requestPath string) bool {
	if len(matcher.Prefixes) > 0 {
		for _, prefix := range matcher.Prefixes {
			if strings.HasPrefix(requestPath, prefix) {
				return true
			}
		}
		return false
	}
	if matcher.Exact != "" {
		return requestPath == matcher.Exact
	}
	if matcher.Regex != nil {
		return matcher.Regex.MatchString(requestPath)
	}
	return true
}

func (matcher PathMatcher) MatchesRequest(host, method, requestPath string, headers, query map[string][]string) bool {
	if !matcher.Matches(requestPath) {
		return false
	}
	if matcher.Host != nil && !matcher.Host.Matches(host, true) {
		return false
	}
	if matcher.Method != nil && !matcher.Method.Matches(method, true) {
		return false
	}
	for name, expected := range matcher.Headers {
		var values []string
		exists := false
		for actual, candidates := range headers {
			if strings.EqualFold(actual, name) {
				values, exists = candidates, true
				break
			}
		}
		if !expected.MatchesAny(values, exists) {
			return false
		}
	}
	for name, expected := range matcher.Query {
		values, exists := query[name]
		if !expected.MatchesAny(values, exists) {
			return false
		}
	}
	return true
}
