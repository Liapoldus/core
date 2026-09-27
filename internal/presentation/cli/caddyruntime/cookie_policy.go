package caddyruntime

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

func (activator *lazyCaddyActivator) ActivatePluginCookiePolicy(ctx context.Context, policy models.PluginCookiePolicy) (func(context.Context) error, error) {
	activator.mu.Lock()
	defer activator.mu.Unlock()
	if activator.active == nil || activator.replace == nil || activator.policyVersion <= 0 {
		return nil, errors.New(activator.unavailable)
	}
	previous := clonePluginDispatchBindings(activator.bindings)
	candidate := clonePluginDispatchBindings(activator.bindings)
	found := false
	for index := range candidate {
		if candidate[index].Name != policy.InstanceID {
			continue
		}
		found = true
		encoded, err := json.Marshal(plugins.CookiePolicy{
			Version: activator.policyVersion, InstanceID: policy.InstanceID,
			Capability: policy.Capability, AllowedNames: policy.AllowedNames,
		})
		if err != nil {
			return nil, errors.New(activator.unavailable)
		}
		filtered := make([]json.RawMessage, 0, len(candidate[index].CookiePolicies)+1)
		for _, existing := range candidate[index].CookiePolicies {
			decoded, decodeErr := plugins.DecodeCookiePolicy(existing)
			if decodeErr != nil {
				return nil, errors.New(activator.unavailable)
			}
			if decoded.Capability != policy.Capability {
				filtered = append(filtered, existing)
			}
		}
		candidate[index].CookiePolicies = append(filtered, encoded)
	}
	if !found {
		return nil, errors.New(activator.unavailable)
	}
	if err := activator.replace(ctx, activator.active, activator.source, candidate); err != nil {
		return nil, err
	}
	activator.bindings = candidate
	return func(rollbackContext context.Context) error {
		activator.mu.Lock()
		defer activator.mu.Unlock()
		if activator.active == nil || activator.replace == nil {
			return errors.New(activator.unavailable)
		}
		if err := activator.replace(rollbackContext, activator.active, activator.source, previous); err != nil {
			stopErr := activator.active.Stop()
			activator.active = nil
			activator.bindings = clonePluginDispatchBindings(previous)
			return errors.Join(err, stopErr)
		}
		activator.bindings = clonePluginDispatchBindings(previous)
		return nil
	}, nil
}
