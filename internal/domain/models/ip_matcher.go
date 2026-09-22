package models

import (
	"net"
	"net/netip"
)

type IPMatcher struct {
	In       []netip.Prefix
	HasIn    bool
	NotIn    []netip.Prefix
	HasNotIn bool
}

func (matcher IPMatcher) Matches(remoteAddress string) bool {
	address := remoteAddress
	if host, _, err := net.SplitHostPort(remoteAddress); err == nil {
		address = host
	}
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	parsed = parsed.Unmap()
	if matcher.HasIn {
		matched := false
		for _, prefix := range matcher.In {
			if prefix.Contains(parsed) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if matcher.HasNotIn {
		for _, prefix := range matcher.NotIn {
			if prefix.Contains(parsed) {
				return false
			}
		}
	}
	return true
}
