package models

type WAFPolicy struct {
	Rules []WAFRule
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
