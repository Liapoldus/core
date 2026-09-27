package caddyruntime

import (
	"context"
	"errors"
	"sync"

	"github.com/Liapoldus/core/internal/domain/models"
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
