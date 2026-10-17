// Package host exposes the Core runtime host for Go compositions. It reads the same persisted settings as the standalone Core
// binary and delegates all construction to the canonical runtime composition
// root; it does not expose internal stores or create a second SQLite model.
package host

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	coreruntime "github.com/Liapoldus/core/v3/internal/runtime"
)

var ErrInvalidOptions = errors.New("invalid Core host options")

// Options selects one persisted Core state database. Project files, Git and
// deployment remain outside this API and belong to the standalone CLI.
type Options struct {
	StatePath string
	Output    string
}

// Host is one Core runtime. It is a singleton relative to StatePath because
// the normal SQLite state lock remains authoritative.
type Host struct {
	runtime *coreruntime.Host
}

// Start loads persisted settings and starts one Core runtime. The caller must
// call WaitReady before exposing dependent services and must call
// Close when the composed process is shutting down.
func Start(ctx context.Context, options Options) (*Host, error) {
	if ctx == nil || options.StatePath == "" || !filepath.IsAbs(options.StatePath) {
		return nil, ErrInvalidOptions
	}
	words, err := config.LoadRuntime()
	if err != nil {
		return nil, ErrInvalidOptions
	}
	settings, err := coreruntime.StoredSettings(filepath.Clean(options.StatePath))
	if err != nil {
		return nil, ErrInvalidOptions
	}
	runtime, err := coreruntime.Start(ctx, settings.Bootstrap(filepath.Clean(options.StatePath)), coreruntime.RunOptions{
		Output:            options.Output,
		Words:             words,
		WriteFailure:      func(string, int, string, string) {},
		TrafficController: settings.TrafficController,
	})
	if err != nil {
		return nil, ErrInvalidOptions
	}
	return &Host{runtime: runtime}, nil
}

func (host *Host) WaitReady(ctx context.Context) error {
	if host == nil || host.runtime == nil {
		return ErrInvalidOptions
	}
	return host.runtime.WaitReady(ctx)
}

func (host *Host) Ready() bool {
	return host != nil && host.runtime != nil && host.runtime.Ready()
}

func (host *Host) Wait() int {
	if host == nil || host.runtime == nil {
		return 1
	}
	return host.runtime.Wait()
}

func (host *Host) Close() int {
	if host == nil || host.runtime == nil {
		return 1
	}
	return host.runtime.Close()
}
