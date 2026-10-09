// Package cli adapts operator commands to the Core bootstrap use cases.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

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
	command []string
}

func Execute(arguments []string) int {
	parsed, err := parseOptions(arguments)
	if err != nil {
		writeFailure(parsed.output, words.Exits.Arguments, words.Codes.ConfigInvalid, err.Error())
		return words.Exits.Arguments
	}
	return run(parsed)
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
		default:
			result.command = arguments
			return result, nil
		}
	}
	return result, nil
}

func run(options options) int {
	if len(options.command) == 0 {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigInvalid, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}
	switch options.command[0] {
	case "init":
		return initCore(options)
	case "recover-settings":
		return recoverSettings(options)
	case words.Commands.Serve:
		return serve(options)
	case words.Commands.Access:
		return access(options)
	case words.Commands.Database:
		return database(options)
	default:
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigInvalid, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}
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
