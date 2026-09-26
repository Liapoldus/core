package cli

import (
	"context"
	"errors"
	"sync"
)

type lazyCaddyActivator struct {
	mu          sync.RWMutex
	active      CaddyRuntime
	validate    func([]byte) error
	start       func([]byte) (CaddyRuntime, error)
	unavailable string
}

func newLazyCaddyActivator(validate func([]byte) error, start func([]byte) (CaddyRuntime, error), unavailable string) *lazyCaddyActivator {
	return &lazyCaddyActivator{validate: validate, start: start, unavailable: unavailable}
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
		return activator.active.Activate(ctx, source)
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
