package models

type WAFPolicy struct {
	Rules []WAFRule
}

type WAFRule struct {
	When   PathMatcher
	Action WAFAction
}

type WAFAction struct {
	Allow bool
	Deny  *Deny
}

func (policy WAFPolicy) Evaluate(path string) (Deny, bool) {
	for _, rule := range policy.Rules {
		if !rule.When.Matches(path) {
			continue
		}
		if rule.Action.Allow {
			return Deny{}, false
		}
		if rule.Action.Deny != nil {
			return *rule.Action.Deny, true
		}
	}
	return Deny{}, false
}
