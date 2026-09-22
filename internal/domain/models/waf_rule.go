package models

type WAFRule struct {
	When   WAFMatcher
	Action WAFAction
}
