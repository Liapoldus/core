// Package plugins contains process supervision and IPC adapters.
package plugins

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

var ErrPluginNotRunning = errors.New("plugin is not running")

type Spec struct {
	Instance string
	Binary   string
	Args     []string
	Env      []string
	Restart  RestartPolicy
}

type RestartPolicy struct {
	Enabled     bool
	Initial     time.Duration
	Max         time.Duration
	MaxAttempts int
}

func (p RestartPolicy) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if p.Initial <= 0 {
		p.Initial = time.Second
	}
	if p.Max <= 0 {
		p.Max = 30 * time.Second
	}
	d := p.Initial
	for i := 1; i < attempt && d < p.Max; i++ {
		d *= 2
		if d > p.Max {
			d = p.Max
		}
	}
	if d > p.Max {
		return p.Max
	}
	return d
}

type Supervisor struct {
	mu      sync.Mutex
	process map[string]*exec.Cmd
}

func NewSupervisor() *Supervisor { return &Supervisor{process: make(map[string]*exec.Cmd)} }
func (s *Supervisor) Start(ctx context.Context, spec Spec) error {
	if spec.Instance == "" || spec.Binary == "" {
		return errors.New("plugin instance and binary are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.process[spec.Instance]; ok {
		return errors.New("plugin is already running")
	}
	cmd := exec.CommandContext(ctx, spec.Binary, spec.Args...)
	cmd.Env = append(os.Environ(), spec.Env...)
	if err := cmd.Start(); err != nil {
		return err
	}
	s.process[spec.Instance] = cmd
	go func() { _ = cmd.Wait(); s.mu.Lock(); delete(s.process, spec.Instance); s.mu.Unlock() }()
	return nil
}
func (s *Supervisor) Stop(instance string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.process[instance]
	if !ok {
		return ErrPluginNotRunning
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	delete(s.process, instance)
	return nil
}
func (s *Supervisor) Restart(ctx context.Context, spec Spec) error {
	if err := s.Stop(spec.Instance); err != nil && !errors.Is(err, ErrPluginNotRunning) {
		return err
	}
	return s.Start(ctx, spec)
}

// RestartWithBackoff retries process startup with bounded exponential delay.
// It is intentionally explicit: callers decide whether a failed plugin is safe to retry.
func (s *Supervisor) RestartWithBackoff(ctx context.Context, spec Spec) error {
	if !spec.Restart.Enabled {
		return s.Restart(ctx, spec)
	}
	limit := spec.Restart.MaxAttempts
	if limit <= 0 {
		limit = 5
	}
	var err error
	for attempt := 1; attempt <= limit; attempt++ {
		if attempt > 1 {
			timer := time.NewTimer(spec.Restart.Delay(attempt - 1))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		err = s.Restart(ctx, spec)
		if err == nil {
			return nil
		}
	}
	return err
}
