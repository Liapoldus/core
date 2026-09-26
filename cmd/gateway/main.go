package main

import (
	"context"
	"encoding/json"
	"os"

	caddyadapter "github.com/Liapoldus/core/internal/infrastructure/caddy"
	"github.com/Liapoldus/core/internal/presentation/cli"
	caddycore "github.com/caddyserver/caddy/v2"
)

func main() {
	version, _ := caddycore.Version()
	os.Exit(cli.ExecuteWithRuntime(os.Args[1:], cli.RuntimeBindings{
		StartEmbeddedCaddy: func(source []byte, bindings []cli.PluginDispatchBinding) (cli.CaddyRuntime, error) {
			instances := make([]caddyadapter.PluginInstance, 0, len(bindings))
			for _, binding := range bindings {
				instances = append(instances, caddyadapter.PluginInstance{
					Name: binding.Name, Endpoint: binding.Endpoint,
					Timeout: binding.Timeout, StartTimeout: binding.StartTimeout,
					MaxConcurrentCalls: binding.MaxConcurrentCalls,
					CookiePolicies:     append([]json.RawMessage(nil), binding.CookiePolicies...),
				})
			}
			runtime, _, err := caddyadapter.StartCaddyfileWithPlugins(source, instances)
			if err != nil {
				return nil, err
			}
			return runtime, nil
		},
		ReplaceEmbeddedCaddy: func(current cli.CaddyRuntime, source []byte, bindings []cli.PluginDispatchBinding) error {
			instances := make([]caddyadapter.PluginInstance, 0, len(bindings))
			for _, binding := range bindings {
				instances = append(instances, caddyadapter.PluginInstance{
					Name: binding.Name, Endpoint: binding.Endpoint,
					Timeout: binding.Timeout, StartTimeout: binding.StartTimeout,
					MaxConcurrentCalls: binding.MaxConcurrentCalls,
					CookiePolicies:     append([]json.RawMessage(nil), binding.CookiePolicies...),
				})
			}
			return caddyadapter.ReplaceCaddyfileWithPlugins(current, source, instances)
		},
		StartExternalCaddy: func(binary, expectedBuildID, stateDirectory string, source []byte, bindings []cli.PluginDispatchBinding) (cli.CaddyRuntime, error) {
			return caddyadapter.StartExternal(context.Background(), caddyadapter.ExternalOptions{
				Binary: binary, ExpectedBuildID: expectedBuildID, StateDirectory: stateDirectory,
				PluginInstances: pluginInstances(bindings),
			}, source)
		},
		ValidateEmbeddedCaddy: func(source []byte, bindings []cli.PluginDispatchBinding) error {
			instances := make([]caddyadapter.PluginInstance, 0, len(bindings))
			for _, binding := range bindings {
				instances = append(instances, caddyadapter.PluginInstance{
					Name: binding.Name, Endpoint: binding.Endpoint,
					Timeout: binding.Timeout, StartTimeout: binding.StartTimeout,
					MaxConcurrentCalls: binding.MaxConcurrentCalls,
					CookiePolicies:     append([]json.RawMessage(nil), binding.CookiePolicies...),
				})
			}
			return caddyadapter.ValidateCaddyfileWithPlugins(source, instances)
		},
		ValidateExternalCaddy: func(binary string, source []byte, bindings []cli.PluginDispatchBinding) error {
			return caddyadapter.ValidateExternalCaddyfileWithPlugins(context.Background(), binary, source, pluginInstances(bindings))
		},
		CaddyBuildID: version,
		CaddyModules: caddycore.Modules(),
	}))
}

func pluginInstances(bindings []cli.PluginDispatchBinding) []caddyadapter.PluginInstance {
	instances := make([]caddyadapter.PluginInstance, 0, len(bindings))
	for _, binding := range bindings {
		instances = append(instances, caddyadapter.PluginInstance{
			Name: binding.Name, Endpoint: binding.Endpoint,
			Timeout: binding.Timeout, StartTimeout: binding.StartTimeout,
			MaxConcurrentCalls: binding.MaxConcurrentCalls,
			CookiePolicies:     append([]json.RawMessage(nil), binding.CookiePolicies...),
		})
	}
	return instances
}
