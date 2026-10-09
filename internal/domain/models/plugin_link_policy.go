package models

// PluginLinkPolicy is the Core-owned desired state for one caller→target
// instance pair. It is durable in SQLite, carries the monotonic revision used
// for optimistic concurrency (ETag/If-Match), and is published to replicas only
// through the SDK peer directory. Core validates only generic syntax, placement
// and carrier pairing; it never interprets product-specific rule content.
type PluginLinkPolicy struct {
	CallerInstanceID string
	TargetInstanceID string
	Revision         int64
	Rules            []PeerLinkRule
}

// Validate reports whether the policy is a well-formed generic link policy that
// can be stored and published. It is fail-closed: a malformed Core-owned policy
// must never be written, and a corrupted stored policy must never widen access.
func (policy PluginLinkPolicy) Validate() error {
	if !validPeerLinkIdentifier(policy.CallerInstanceID) || !validPeerLinkIdentifier(policy.TargetInstanceID) ||
		policy.CallerInstanceID == policy.TargetInstanceID {
		return PeerLinkPolicyInvalid{}
	}
	if len(policy.Rules) == 0 {
		return PeerLinkPolicyInvalid{}
	}
	seen := make(map[string]struct{}, len(policy.Rules))
	for _, rule := range policy.Rules {
		if rule.CallerInstanceID != policy.CallerInstanceID || rule.TargetInstanceID != policy.TargetInstanceID {
			return PeerLinkPolicyInvalid{}
		}
		if err := rule.Validate(); err != nil {
			return PeerLinkPolicyInvalid{}
		}
		key := rule.PlacementRule + "\x00" + rule.Carrier
		if _, exists := seen[key]; exists {
			return PeerLinkPolicyInvalid{}
		}
		seen[key] = struct{}{}
	}
	return nil
}
