package cli

import "github.com/Liapoldus/core/internal/infrastructure/config"
import bootstrapruntime "github.com/Liapoldus/core/internal/presentation/cli/bootstrap"

func serve(options options, runtime RuntimeBindings) int {
	path, _, err := discoverConfig(options)
	if err != nil {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
		return words.Exits.Arguments
	}
	bootstrap, err := config.LoadBootstrap(path)
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	return bootstrapruntime.Serve(bootstrap, bootstrapruntime.RunOptions{
		Output: options.output, Words: words, RuntimeBindings: runtime, WriteFailure: writeFailure,
	})
}
