package models

import (
	"regexp"
	"slices"
	"strings"
)

type StringMatcher struct {
	Exact     string
	HasExact  bool
	Prefix    string
	HasPrefix bool
	Regex     *regexp.Regexp
	Exists    *bool
	In        []string
	HasIn     bool
	NotIn     []string
	HasNotIn  bool
}

func (matcher StringMatcher) Matches(value string, exists bool) bool {
	if matcher.Exists != nil && *matcher.Exists != exists {
		return false
	}
	if (matcher.HasExact || matcher.Exact != "") && value != matcher.Exact {
		return false
	}
	if matcher.HasPrefix && !strings.HasPrefix(value, matcher.Prefix) {
		return false
	}
	if matcher.Regex != nil && !matcher.Regex.MatchString(value) {
		return false
	}
	if matcher.HasIn && !slices.Contains(matcher.In, value) {
		return false
	}
	if matcher.HasNotIn && slices.Contains(matcher.NotIn, value) {
		return false
	}
	return true
}
