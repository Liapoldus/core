package models

import "strings"

// Placement and carrier spellings owned by the generic Plugin SDK peer
// directory contract. Core carries these opaque values into the published
// directory and never derives plugin-specific placement or transport behavior.
const (
	PeerLinkPlacementSame   = "same-placement"
	PeerLinkPlacementRemote = "remote"

	PeerLinkCarrierTCP         = "tcp"
	PeerLinkCarrierQUIC        = "quic"
	PeerLinkCarrierUnix        = "unix"
	PeerLinkCarrierWindowsPipe = "windows-named-pipe"

	PeerLinkMaximumWeight = 100
)

// PeerLinkRule is one Core-owned, deny-by-default authorization for a caller
// instance to reach a target instance over a named carrier. A replica receives
// a peer-directory link only when a matching rule exists; the absence of any
// rule denies the link entirely. Rules are generic: Core never encodes plugin,
// capability or product names.
type PeerLinkRule struct {
	CallerInstanceID  string
	TargetInstanceID  string
	PlacementRule     string
	Carrier           string
	Weight            uint16
	RequiredContracts []PeerContractRange
}

// Validate reports whether the rule is a well-formed generic link policy. It is
// used both when authoring a rule and, fail-closed, when assembling a directory
// so a corrupted Core-owned policy can never widen access accidentally.
func (rule PeerLinkRule) Validate() error {
	if !validPeerLinkIdentifier(rule.CallerInstanceID) || !validPeerLinkIdentifier(rule.TargetInstanceID) ||
		rule.CallerInstanceID == rule.TargetInstanceID {
		return PeerLinkRuleInvalid{}
	}
	if !validPeerLinkPlacementCarrier(rule.PlacementRule, rule.Carrier) {
		return PeerLinkRuleInvalid{}
	}
	if rule.Weight == 0 || rule.Weight > PeerLinkMaximumWeight {
		return PeerLinkRuleInvalid{}
	}
	seen := make(map[string]struct{}, len(rule.RequiredContracts))
	for _, requirement := range rule.RequiredContracts {
		if !validPeerLinkIdentifier(requirement.ContractID) {
			return PeerLinkRuleInvalid{}
		}
		if _, exists := seen[requirement.ContractID]; exists {
			return PeerLinkRuleInvalid{}
		}
		seen[requirement.ContractID] = struct{}{}
	}
	return nil
}

func validPeerLinkIdentifier(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character > 127 {
			return false
		}
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' ||
			character == '.' || character == '~' {
			continue
		}
		return false
	}
	return true
}

func validPeerLinkPlacementCarrier(placement, carrier string) bool {
	switch placement {
	case PeerLinkPlacementSame:
		return carrier == PeerLinkCarrierUnix || carrier == PeerLinkCarrierWindowsPipe
	case PeerLinkPlacementRemote:
		return carrier == PeerLinkCarrierTCP || carrier == PeerLinkCarrierQUIC
	default:
		return false
	}
}
