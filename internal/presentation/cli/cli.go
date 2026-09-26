// Package cli adapts operator commands to the Gateway bootstrap use cases.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/infrastructure/config"
)

var words = func() config.CLIWords {
	loaded, err := config.LoadCLI()
	if err != nil {
		panic(err)
	}
	return loaded
}()

type options struct {
	output  string
	config  string
	command []string
}

func Execute(arguments []string) int {
	return ExecuteWithRuntime(arguments, RuntimeBindings{})
}

type RuntimeBindings struct {
	StartEmbeddedCaddy func([]byte, []PluginDispatchBinding) (CaddyRuntime, error)
	StartExternalCaddy func(binary, expectedBuildID, stateDirectory string, source []byte) (CaddyRuntime, error)
	CaddyBuildID       string
	CaddyModules       []string
}

type PluginDispatchBinding struct {
	Name               string
	Endpoint           string
	Timeout            time.Duration
	StartTimeout       time.Duration
	MaxConcurrentCalls int
}

type CaddyRuntime interface {
	interfaces.CaddySnapshotActivator
	Stop() error
}

func ExecuteWithRuntime(arguments []string, runtime RuntimeBindings) int {
	parsed, err := parseOptions(arguments)
	if err != nil {
		writeFailure(parsed.output, words.Exits.Arguments, words.Codes.ConfigInvalid, err.Error())
		return words.Exits.Arguments
	}
	return run(parsed, runtime)
}

func parseOptions(arguments []string) (options, error) {
	result := options{output: words.Outputs.Text}
	for len(arguments) > 0 {
		switch arguments[0] {
		case words.Flags.Output:
			if len(arguments) < 2 || arguments[1] != words.Outputs.Text && arguments[1] != words.Outputs.JSON {
				return options{}, errors.New(words.Diagnostics.OutputInvalid)
			}
			result.output = arguments[1]
			arguments = arguments[2:]
		case words.Flags.Config:
			if len(arguments) < 2 {
				return options{}, errors.New(words.Diagnostics.ConfigRequired)
			}
			result.config = arguments[1]
			arguments = arguments[2:]
		default:
			result.command = arguments
			return result, nil
		}
	}
	return result, nil
}

func run(options options, runtime RuntimeBindings) int {
	if len(options.command) == 0 {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigInvalid, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}
	switch options.command[0] {
	case words.Commands.Serve:
		return serve(options, runtime)
	case words.Commands.Access:
		return access(options)
	default:
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigInvalid, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}
}

func discoverConfig(options options) (string, string, error) {
	if options.config != "" {
		path, err := absoluteExistingFile(options.config)
		return path, words.Sources.Flag, err
	}
	if path := os.Getenv(words.Environment.GatewayConfig); path != "" {
		resolved, err := absoluteExistingFile(path)
		return resolved, words.Sources.Environment, err
	}
	path, err := absoluteExistingFile(words.Paths.DefaultConfig)
	return path, words.Sources.System, err
}

func absoluteExistingFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errors.New(words.Diagnostics.ConfigLookupFailed)
	}
	return filepath.Abs(path)
}

func configValidationFailure(output string, _ error) int {
	writeFailure(output, words.Exits.Validation, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
	return words.Exits.Validation
}

func writeSuccess(output string, value map[string]any) {
	if output == words.Outputs.JSON {
		_ = json.NewEncoder(os.Stdout).Encode(value)
		return
	}
	fmt.Println(words.Text.OK)
}

func writeFailure(output string, exitCode int, code, detail string) {
	if output == words.Outputs.JSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			words.JSON.OK: false,
			words.JSON.Problem: map[string]any{
				words.JSON.Code:   code,
				words.JSON.Detail: detail,
			},
		})
		return
	}
	fmt.Fprintln(os.Stderr, detail)
}
