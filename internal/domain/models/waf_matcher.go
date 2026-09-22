package models

import "strings"

type WAFMatcher struct {
	Path     PathMatcher
	Method   *StringMatcher
	SourceIP *IPMatcher
	Headers  map[string]StringMatcher
	Query    map[string]StringMatcher
	Geo      *GeoMatcher
	ASN      *ASNMatcher
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

func (matcher WAFMatcher) MatchesResolved(request WAFRequest, geo map[string]GeoRecord) bool {
	if !matcher.Matches(request) {
		return false
	}
	if matcher.Geo != nil {
		record, ok := geo[matcher.Geo.Provider]
		if !ok || matcher.Geo.Country != nil && !matcher.Geo.Country.Matches(record.Country, false) || matcher.Geo.City != nil && !matcher.Geo.City.Matches(record.City, false) {
			return false
		}
	}
	if matcher.ASN != nil {
		record, ok := geo[matcher.ASN.Provider]
		if !ok || record.ASN == nil || !numberIn(*record.ASN, matcher.ASN.In, matcher.ASN.NotIn) {
			return false
		}
	}
	return true
}

func numberIn(value uint, included, excluded []uint) bool {
	if len(included) > 0 {
		for _, candidate := range included {
			if candidate == value {
				return !uintContains(excluded, value)
			}
		}
		return false
	}
	return !uintContains(excluded, value)
}

func uintContains(values []uint, value uint) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
