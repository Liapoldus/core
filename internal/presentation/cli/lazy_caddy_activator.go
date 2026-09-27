package cli

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
)

type lazyCaddyActivator struct {
	mu            sync.RWMutex
	active        CaddyRuntime
	validate      func([]byte) error
	start         func([]byte) (CaddyRuntime, error)
	unavailable   string
	bindings      []PluginDispatchBinding
	source        []byte
	replace       func(context.Context, CaddyRuntime, []byte, []PluginDispatchBinding) error
	policyVersion int
}

func newLazyCaddyActivator(validate func([]byte) error, start func([]byte) (CaddyRuntime, error), unavailable string, bindings []PluginDispatchBinding, replace func(context.Context, CaddyRuntime, []byte, []PluginDispatchBinding) error, policyVersion int) *lazyCaddyActivator {
	return &lazyCaddyActivator{validate: validate, start: start, unavailable: unavailable, bindings: clonePluginDispatchBindings(bindings), replace: replace, policyVersion: policyVersion}
}

func (activator *lazyCaddyActivator) Validate(ctx context.Context, source []byte) error {
	activator.mu.RLock()
	active := activator.active
	validate := activator.validate
	activator.mu.RUnlock()
	if active != nil {
		return active.Validate(ctx, source)
	}
	if validate == nil {
		return errors.New(activator.unavailable)
	}
	return validate(source)
}

func (activator *lazyCaddyActivator) Activate(ctx context.Context, source []byte) error {
	activator.mu.Lock()
	defer activator.mu.Unlock()
	if activator.active != nil {
		if err := activator.active.Activate(ctx, source); err != nil {
			return err
		}
		activator.source = append([]byte(nil), source...)
		return nil
	}
	if activator.start == nil {
		return errors.New(activator.unavailable)
	}
	if activator.validate == nil {
		return errors.New(activator.unavailable)
	}
	if err := activator.validate(source); err != nil {
		return err
	}
	runtime, err := activator.start(source)
	if err != nil {
		return err
	}
	activator.active = runtime
	activator.source = append([]byte(nil), source...)
	return nil
}

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

func (activator *lazyCaddyActivator) Stop() error {
	activator.mu.Lock()
	defer activator.mu.Unlock()
	if activator.active == nil {
		return nil
	}
	err := activator.active.Stop()
	activator.active = nil
	return err
}

func (activator *lazyCaddyActivator) Active() bool {
	activator.mu.RLock()
	defer activator.mu.RUnlock()
	return activator.active != nil
}

func (activator *lazyCaddyActivator) Ready(ctx context.Context) error {
	activator.mu.RLock()
	active := activator.active
	activator.mu.RUnlock()
	if active == nil {
		return errors.New(activator.unavailable)
	}
	probe, ok := active.(interface{ Ready(context.Context) error })
	if !ok {
		return nil
	}
	return probe.Ready(ctx)
}

func (activator *lazyCaddyActivator) Snapshot(ctx context.Context) ([]byte, error) {
	activator.mu.RLock()
	active := activator.active
	activator.mu.RUnlock()
	admin, ok := active.(interface {
		Snapshot(context.Context) ([]byte, error)
	})
	if !ok {
		return nil, errors.New(activator.unavailable)
	}
	return admin.Snapshot(ctx)
}

func (activator *lazyCaddyActivator) Request(ctx context.Context, request models.CaddyAdminRequest) (models.CaddyAdminResponse, error) {
	activator.mu.RLock()
	active := activator.active
	activator.mu.RUnlock()
	admin, ok := active.(interface {
		Request(context.Context, models.CaddyAdminRequest) (models.CaddyAdminResponse, error)
	})
	if !ok {
		return models.CaddyAdminResponse{}, errors.New(activator.unavailable)
	}
	return admin.Request(ctx, request)
}

func clonePluginDispatchBindings(bindings []PluginDispatchBinding) []PluginDispatchBinding {
	cloned := make([]PluginDispatchBinding, len(bindings))
	for index, binding := range bindings {
		cloned[index] = binding
		cloned[index].CookiePolicies = append([]json.RawMessage(nil), binding.CookiePolicies...)
	}
	return cloned
}
