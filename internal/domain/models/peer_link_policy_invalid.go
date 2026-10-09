package models

// PeerLinkPolicyInvalid reports that a Core-owned caller→target peer link
// policy is malformed and must be rejected without widening access.
type PeerLinkPolicyInvalid struct{}

func (PeerLinkPolicyInvalid) Error() string {
	return "invalid peer link policy"
}
