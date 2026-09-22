package models

type WAFPolicy struct {
	Rules []WAFRule
}

func (policy WAFPolicy) Evaluate(request WAFRequest) (WAFAction, bool) {
	return policy.EvaluateResolved(request, nil)
}

func (policy WAFPolicy) EvaluateResolved(request WAFRequest, geo map[string]GeoRecord) (WAFAction, bool) {
	for _, rule := range policy.Rules {
		if !rule.When.MatchesResolved(request, geo) {
			continue
		}
		return rule.Action, true
	}
	return WAFAction{}, false
}
