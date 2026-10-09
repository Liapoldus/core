package models

// PeerLinkPolicyUnavailable reports that the durable peer link policy store
// could not serve the request. It carries no driver or SQL detail.
type PeerLinkPolicyUnavailable struct {
	Message string
}

func (failure PeerLinkPolicyUnavailable) Error() string {
	return failure.Message
}
