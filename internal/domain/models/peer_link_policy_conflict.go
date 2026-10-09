package models

// PeerLinkPolicyConflict reports that a policy already exists for the pair, or
// that a conditional mutation carried a stale expected revision.
type PeerLinkPolicyConflict struct{}

func (PeerLinkPolicyConflict) Error() string {
	return "peer link policy conflict"
}
