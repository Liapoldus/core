package caddyruntime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
)

type RuntimeBindings struct {
	StartEmbeddedCaddy    func([]byte, []PluginDispatchBinding) (CaddyRuntime, error)
	ReplaceEmbeddedCaddy  func(context.Context, CaddyRuntime, []byte, []PluginDispatchBinding) error
	ReplaceExternalCaddy  func(context.Context, CaddyRuntime, []byte, []PluginDispatchBinding) error
	StartExternalCaddy    func(binary, expectedBuildID, stateDirectory string, source []byte, bindings []PluginDispatchBinding) (CaddyRuntime, error)
	ValidateEmbeddedCaddy func([]byte, []PluginDispatchBinding) error
	ValidateExternalCaddy func(binary string, source []byte, bindings []PluginDispatchBinding) error
	CaddyBuildID          string
	CaddyModules          []string
}

type PluginDispatchBinding struct {
	Name               string
	Endpoint           string
	Timeout            time.Duration
	StartTimeout       time.Duration
	MaxConcurrentCalls int
	CookiePolicies     []json.RawMessage
}

type CaddyRuntime interface {
	interfaces.CaddySnapshotActivator
	Stop() error
}

func clonePluginDispatchBindings(bindings []PluginDispatchBinding) []PluginDispatchBinding {
	cloned := make([]PluginDispatchBinding, len(bindings))
	for index, binding := range bindings {
		cloned[index] = binding
		cloned[index].CookiePolicies = append([]json.RawMessage(nil), binding.CookiePolicies...)
	}
	return cloned
}
