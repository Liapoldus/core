package cli

import (
	"github.com/Liapoldus/core/internal/infrastructure/config"
	bootstrapruntime "github.com/Liapoldus/core/internal/presentation/cli/bootstrap"
)

func serve(options options) int {
	if len(options.command) != 1 {
		return configValidationFailure(options.output, config.ErrInvalidDocument)
	}
	path, err := statePath()
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	settings, err := storedSettings(path)
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	return bootstrapruntime.Serve(settings.Bootstrap(path), bootstrapruntime.RunOptions{
		Output: options.output, Words: words, WriteFailure: writeFailure, TrafficController: settings.TrafficController,
	})
}
