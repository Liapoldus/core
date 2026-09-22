package models

import (
	"regexp"
	"strings"
)

type PathMatcher struct {
	Prefixes []string
	Exact    string
	Regex    *regexp.Regexp
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
