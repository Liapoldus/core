package network

import (
	"net/http"
	"sync"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type WAFRuntime struct {
	mu       sync.RWMutex
	policies map[string]models.WAFPolicy
	lookup   interfaces.GeoLookup
}

func NewWAFRuntime(policies map[string]models.WAFPolicy, lookup interfaces.GeoLookup) *WAFRuntime {
	return &WAFRuntime{policies: policies, lookup: lookup}
}

func (runtime *WAFRuntime) Replace(policies map[string]models.WAFPolicy, lookup interfaces.GeoLookup) {
	runtime.mu.Lock()
	runtime.policies = policies
	runtime.lookup = lookup
	runtime.mu.Unlock()
}

func (runtime *WAFRuntime) Evaluate(name string, request models.WAFRequest) (models.WAFAction, bool) {
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	policy, exists := runtime.policies[name]
	if !exists {
		return models.WAFAction{Deny: &models.Deny{Status: http.StatusServiceUnavailable}}, true
	}
	return evaluateWAF(policy, request, runtime.lookup)
}
