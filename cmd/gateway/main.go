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
		StartEmbeddedCaddy: func(source []byte) (cli.CaddyRuntime, error) {
			runtime, _, err := caddyadapter.StartCaddyfile(source)
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
