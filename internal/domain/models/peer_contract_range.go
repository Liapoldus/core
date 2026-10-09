package models

// PeerContractRange is a generic, opaque compatibility requirement a caller
// instance declares for one target peer. The contract identifier and versions
// are owned by the plugins that speak them; Core neither interprets nor
// version-resolves them, it only carries the requirement into the published
// peer directory.
type PeerContractRange struct {
	ContractID              string
	MinimumVersion          string
	MaximumVersionExclusive string
}
