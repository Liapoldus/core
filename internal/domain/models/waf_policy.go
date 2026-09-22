package models

type WAFPolicy struct {
	Rules []WAFRule
}

func (policy WAFPolicy) Evaluate(path, method, remoteAddress string) (WAFAction, bool) {
	for _, rule := range policy.Rules {
		if !rule.When.Matches(path, method, remoteAddress) {
			continue
		}
		return rule.Action, true
	}
	return WAFAction{}, false
}
