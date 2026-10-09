package config

import (
	"net/url"
	"strings"
)

// PluginEndpoint validates one operator-declared replica origin. Core dials
// exactly this address and never learns it from a redirect or advertisement.
func PluginEndpoint(endpoint string) (*url.URL, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, ErrInvalidDocument
	}
	return parsed, nil
}

const peerIdentityMaximumBytes = 256

// ValidPeerIdentity checks one declared replica identity: an explicit common
// name plus an optional absolute URI.
func ValidPeerIdentity(commonName, uniformResourceIdentifier string) error {
	if commonName == "" || len(commonName) > peerIdentityMaximumBytes || strings.TrimSpace(commonName) != commonName {
		return ErrInvalidDocument
	}
	if uniformResourceIdentifier == "" {
		return nil
	}
	parsed, err := url.Parse(uniformResourceIdentifier)
	if err != nil || !parsed.IsAbs() || parsed.Scheme == "" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.HasPrefix(parsed.Path, "/") || len(uniformResourceIdentifier) > peerIdentityMaximumBytes {
		return ErrInvalidDocument
	}
	return nil
}
