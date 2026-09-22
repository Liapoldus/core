package models

type WAFRule struct {
	When            WAFMatcher
	Action          WAFAction
	OnErrorAllow    bool
	OnErrorExplicit bool
}
