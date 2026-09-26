package main

import (
	"context"
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
				})
			}
			runtime, _, err := caddyadapter.StartCaddyfileWithPlugins(source, instances)
			if err != nil {
				return nil, err
			}
			return runtime, nil
		},
		StartExternalCaddy: func(binary, expectedBuildID, stateDirectory string, source []byte) (cli.CaddyRuntime, error) {
			return caddyadapter.StartExternal(context.Background(), caddyadapter.ExternalOptions{
				Binary: binary, ExpectedBuildID: expectedBuildID, StateDirectory: stateDirectory,
			}, source)
		},
		CaddyBuildID: version,
		CaddyModules: caddycore.Modules(),
	}))
}
