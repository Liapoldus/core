package models

type WAFRule struct {
	When   PathMatcher
	Action WAFAction
}
