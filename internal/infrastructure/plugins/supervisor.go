// Package plugins contains process supervision and IPC adapters.
package plugins

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
)

var ErrPluginNotRunning = errors.New("plugin is not running")

type Spec struct {
	Instance string
	Binary   string
	Args     []string
	Env      []string
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
