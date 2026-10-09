package models

// PeerLinkPolicyNotFound reports that no durable policy exists for the
// requested caller→target instance pair.
type PeerLinkPolicyNotFound struct{}

func (PeerLinkPolicyNotFound) Error() string {
	return "peer link policy not found"
}
