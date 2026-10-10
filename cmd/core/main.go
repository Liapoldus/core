package main

import (
	"fmt"
	"os"

	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	runtime "github.com/Liapoldus/core/v3/internal/runtime"
)

func main() {
	words, err := config.LoadRuntime()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Core runtime contract unavailable")
		os.Exit(1)
	}
	path, err := runtime.StatePath()
	if err != nil {
		fmt.Fprintln(os.Stderr, "CORE_SQLITE_PATH must be an absolute path")
		os.Exit(words.Exits.Arguments)
	}
	if err := runtime.EnsureInitialized(path); err != nil {
		fmt.Fprintln(os.Stderr, "Core state initialization failed")
		os.Exit(words.Exits.Validation)
	}
	settings, err := runtime.StoredSettings(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Core settings are unavailable")
		os.Exit(words.Exits.Validation)
	}
	os.Exit(runtime.Serve(settings.Bootstrap(path), runtime.RunOptions{
		Output: "text", Words: words, WriteFailure: func(_ string, _ int, _, detail string) {
			fmt.Fprintln(os.Stderr, detail)
		}, TrafficController: settings.TrafficController,
	}))
}
