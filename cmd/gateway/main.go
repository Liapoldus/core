package main

import (
	"context"
	"encoding/json"
	"os"
	"time"

	caddyadapter "github.com/Liapoldus/core/internal/infrastructure/caddy"
	"github.com/Liapoldus/core/internal/infrastructure/config"
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
			adminOptions, err := loadAdminRuntimeOptions()
			if err != nil {
				return nil, err
			}
			runtime, _, err := caddyadapter.StartCaddyfileWithPluginsAndAdmin(source, instances, adminOptions)
			if err != nil {
				return nil, err
			}
			return runtime, nil
		},
		ReplaceEmbeddedCaddy: func(_ context.Context, current cli.CaddyRuntime, source []byte, bindings []cli.PluginDispatchBinding) error {
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
		ReplaceExternalCaddy: func(ctx context.Context, current cli.CaddyRuntime, source []byte, bindings []cli.PluginDispatchBinding) error {
			return caddyadapter.ReplaceExternalSnapshotWithPlugins(ctx, current, source, pluginInstances(bindings))
		},
		StartExternalCaddy: func(binary, expectedBuildID, stateDirectory string, source []byte, bindings []cli.PluginDispatchBinding) (cli.CaddyRuntime, error) {
			adminOptions, err := loadAdminRuntimeOptions()
			if err != nil {
				return nil, err
			}
			return caddyadapter.StartExternal(context.Background(), caddyadapter.ExternalOptions{
				Binary: binary, ExpectedBuildID: expectedBuildID, StateDirectory: stateDirectory,
				PluginInstances: pluginInstances(bindings), Admin: adminOptions,
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

func loadAdminRuntimeOptions() (caddyadapter.AdminRuntimeOptions, error) {
	words, err := config.LoadAdminMutation()
	if err != nil {
		return caddyadapter.AdminRuntimeOptions{}, err
	}
	timeout, err := time.ParseDuration(words.Timeouts.Request)
	if err != nil {
		return caddyadapter.AdminRuntimeOptions{}, err
	}
	return caddyadapter.AdminRuntimeOptions{
		Client: caddyadapter.AdminClientOptions{
			SnapshotPath: words.Paths.Snapshot, PathPrefix: words.Paths.LeadingSlash,
			RequestBodyBytes: words.Limits.RequestBodyBytes, SnapshotBytes: words.Limits.SnapshotBytes,
			ResponseBodyBytes: words.Limits.ResponseBodyBytes, RequestTimeout: timeout,
			ForwardRequestHeaders: words.Headers.ForwardRequest, ForwardResponseHeaders: words.Headers.ForwardResponse,
			InvalidConfiguration: words.Diagnostics.InvalidConfiguration,
			SnapshotUnavailable:  words.Diagnostics.SnapshotUnavailable, AdminUnavailable: words.Diagnostics.AdminUnavailable,
		},
		UnixPrefix: words.Paths.UnixPrefix, UnixNetwork: words.Paths.UnixNetwork,
		URLScheme: words.Paths.URLScheme, URLHost: words.Paths.URLHost,
		SocketDirectoryPrefix: words.Socket.DirectoryPrefix, SocketName: words.Socket.Name,
		DirectoryMode: words.Modes.Directory, SocketMode: words.Modes.Socket,
	}, nil
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
