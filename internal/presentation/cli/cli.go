// Package cli adapts operator commands to Gateway application services.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/network"
)

var words = func() config.CLIWords {
	loaded, err := config.LoadCLI()
	if err != nil {
		panic(err)
	}
	return loaded
}()

type options struct {
	output       string
	config       string
	configDir    string
	noManagement bool
	command      []string
}

func Execute(arguments []string) int {
	options, err := parseOptions(arguments)
	if err != nil {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, err.Error())
		return words.Exits.Arguments
	}
	return run(options)
}

func parseOptions(arguments []string) (options, error) {
	result := options{output: words.Outputs.Text}
	for len(arguments) > 0 {
		switch arguments[0] {
		case words.Flags.Output:
			if len(arguments) < 2 || (arguments[1] != words.Outputs.Text && arguments[1] != words.Outputs.JSON) {
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
		case words.Flags.ConfigDir:
			if len(arguments) < 2 {
				return options{}, errors.New(words.Diagnostics.ConfigDirRequired)
			}
			result.configDir = arguments[1]
			arguments = arguments[2:]
		case words.Flags.NoManagement:
			result.noManagement = true
			arguments = arguments[1:]
		default:
			result.command = arguments
			return result, nil
		}
	}
	return result, nil
}

func run(options options) int {
	if len(options.command) == 0 {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}
	if options.command[0] == words.Commands.Serve {
		return serve(options)
	}
	if len(options.command) < 2 || options.command[0] != words.Commands.Config {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}

	switch options.command[1] {
	case words.Subcommands.Path:
		path, source, err := discoverConfig(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		writeSuccess(options.output, map[string]any{
			words.JSON.OK: true, words.JSON.Command: words.Display.Path, words.JSON.Path: path, words.JSON.Source: source,
		})
		return words.Exits.OK
	case words.Subcommands.Validate:
		path, err := configForValidation(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		code := words.Codes.ConfigInvalid
		if err := config.Validate(path); err != nil {
			if config.IsUnknownField(err) {
				code = words.Codes.UnknownField
			}
			writeFailure(options.output, words.Exits.Validation, code, words.Diagnostics.ConfigInvalid)
			return words.Exits.Validation
		}
		writeSuccess(options.output, map[string]any{
			words.JSON.OK: true, words.JSON.Command: words.Display.Validate, words.JSON.Valid: true,
		})
		return words.Exits.OK
	case words.Subcommands.Print:
		path, err := configForValidation(options)
		if err != nil {
			writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
			return words.Exits.Arguments
		}
		if options.output == words.Outputs.JSON {
			document, err := config.PrintJSON(path)
			if err != nil {
				return configValidationFailure(options.output, err)
			}
			writeSuccess(options.output, map[string]any{
				words.JSON.OK: true, words.JSON.Command: words.Display.Print, words.JSON.Document: document,
			})
			return words.Exits.OK
		}
		document, err := config.PrintDocument(path)
		if err != nil {
			return configValidationFailure(options.output, err)
		}
		fmt.Println(strings.TrimRight(string(document), "\n"))
		return words.Exits.OK
	default:
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.UnknownConfigCommand)
		return words.Exits.Arguments
	}
}

func serve(options options) int {
	path, _, err := discoverConfig(options)
	if err != nil {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
		return words.Exits.Arguments
	}
	graph, err := config.CompileGateway(path)
	if err != nil {
		code := words.Codes.ConfigInvalid
		if config.IsUnknownField(err) {
			code = words.Codes.UnknownField
		}
		writeFailure(options.output, words.Exits.Validation, code, words.Diagnostics.ConfigInvalid)
		return words.Exits.Validation
	}
	drain, err := time.ParseDuration(words.Serve.GracefulTimeout)
	if err != nil {
		writeFailure(options.output, words.Exits.Validation, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Validation
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := network.Serve(ctx, graph.Listeners, graph.Sites, drain); err != nil {
		writeFailure(options.output, words.Exits.Validation, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Validation
	}
	return words.Exits.OK
}

func configForValidation(options options) (string, error) {
	if len(options.command) > 2 {
		return absoluteExistingFile(options.command[2])
	}
	path, _, err := discoverConfig(options)
	return path, err
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
	if options.configDir != "" {
		path, err := absoluteExistingFile(filepath.Join(options.configDir, words.Paths.FileName))
		return path, words.Sources.FlagDirectory, err
	}
	if directory := os.Getenv(words.Environment.ConfigDir); directory != "" {
		path, err := absoluteExistingFile(filepath.Join(directory, words.Paths.FileName))
		return path, words.Sources.EnvironmentDirectory, err
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

func configValidationFailure(output string, err error) int {
	code := words.Codes.ConfigInvalid
	if config.IsUnknownField(err) {
		code = words.Codes.UnknownField
	}
	writeFailure(output, words.Exits.Validation, code, words.Diagnostics.ConfigInvalid)
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