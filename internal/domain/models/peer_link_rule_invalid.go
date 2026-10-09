package models

// PeerLinkRuleInvalid reports that a Core-owned peer link rule is malformed and
// must be rejected without widening access.
type PeerLinkRuleInvalid struct{}

func (PeerLinkRuleInvalid) Error() string {
	return "invalid peer link rule"
}
