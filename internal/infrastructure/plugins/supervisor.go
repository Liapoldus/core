// Package plugins contains process supervision and IPC adapters.
package plugins

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

var ErrPluginNotRunning = errors.New("plugin is not running")

type Spec struct {
	Instance string
	Binary   string
	Listener *net.TCPListener
	Restart  RestartPolicy
}

type RestartPolicy struct {
	Enabled bool
	Initial time.Duration
	Max     time.Duration
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
	process map[string]*supervisedProcess
}

type supervisedProcess struct {
	command *exec.Cmd
	done    chan error
}

func NewSupervisor() *Supervisor { return &Supervisor{process: make(map[string]*supervisedProcess)} }

func (s *Supervisor) StartWithExit(ctx context.Context, spec Spec) (<-chan error, error) {
	if spec.Instance == "" || spec.Binary == "" {
		return nil, errors.New("plugin instance and binary are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.process[spec.Instance]; ok {
		return nil, errors.New("plugin is already running")
	}
	cmd := exec.CommandContext(ctx, spec.Binary)
	cmd.Env = []string{}
	if spec.Listener != nil {
		listenerFile, err := spec.Listener.File()
		if err != nil {
			return nil, err
		}
		cmd.ExtraFiles = []*os.File{listenerFile}
		defer listenerFile.Close()
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process := &supervisedProcess{command: cmd, done: make(chan error, 1)}
	s.process[spec.Instance] = process
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		if s.process[spec.Instance] == process {
			delete(s.process, spec.Instance)
		}
		s.mu.Unlock()
		process.done <- err
		close(process.done)
	}()
	return process.done, nil
}

func (s *Supervisor) Stop(instance string) error {
	s.mu.Lock()
	process, ok := s.process[instance]
	if !ok {
		s.mu.Unlock()
		return ErrPluginNotRunning
	}
	delete(s.process, instance)
	s.mu.Unlock()
	if process.command.Process != nil {
		if err := process.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	<-process.done
	return nil
}

func (s *Supervisor) ResidentMemory(instance string) (uint64, error) {
	s.mu.Lock()
	managed, ok := s.process[instance]
	if !ok || managed.command.Process == nil {
		s.mu.Unlock()
		return 0, ErrPluginNotRunning
	}
	pid := int32(managed.command.Process.Pid)
	s.mu.Unlock()
	processInfo, err := process.NewProcess(pid)
	if err != nil {
		return 0, err
	}
	memory, err := processInfo.MemoryInfo()
	if err != nil {
		return 0, err
	}
	return memory.RSS, nil
}
