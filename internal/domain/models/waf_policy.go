package models

type WAFPolicy struct {
	Rules []WAFRule
}

func (policy WAFPolicy) Evaluate(request WAFRequest) (WAFAction, bool) {
	for _, rule := range policy.Rules {
		if !rule.When.Matches(request) {
			continue
		}
		return rule.Action, true
	}
	return WAFAction{}, false
}
