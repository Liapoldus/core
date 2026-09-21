package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Liapoldus/core/internal/infrastructure/validation"
)

const (
	exitOK             = 0
	exitConfigNotFound = 2
	exitValidation     = 3
)

type options struct {
	output    string
	config    string
	configDir string
	command   []string
}

type problem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func main() {
	options, err := parseOptions(os.Args[1:])
	if err != nil {
		writeFailure("text", exitConfigNotFound, "config_not_found", err.Error())
		return
	}

	exitCode := run(options)
	os.Exit(exitCode)
}

func parseOptions(arguments []string) (options, error) {
	result := options{output: "text"}
	for len(arguments) > 0 {
		switch arguments[0] {
		case "--output":
			if len(arguments) < 2 || (arguments[1] != "text" && arguments[1] != "json") {
				return options{}, errors.New("--output must be text or json")
			}
			result.output = arguments[1]
			arguments = arguments[2:]
		case "--config":
			if len(arguments) < 2 {
				return options{}, errors.New("--config requires a path")
			}
			result.config = arguments[1]
			arguments = arguments[2:]
		case "--config-dir":
			if len(arguments) < 2 {
				return options{}, errors.New("--config-dir requires a path")
			}
			result.configDir = arguments[1]
			arguments = arguments[2:]
		default:
			result.command = arguments
			return result, nil
		}
	}
	return result, nil
}

func run(options options) int {
	if len(options.command) < 2 || options.command[0] != "config" {
		writeFailure(options.output, exitConfigNotFound, "config_not_found", "ожидается команда config")
		return exitConfigNotFound
	}

	switch options.command[1] {
	case "path":
		path, source, err := discoverConfig(options)
		if err != nil {
			writeFailure(options.output, exitConfigNotFound, "config_not_found", "Файл конфигурации не найден.")
			return exitConfigNotFound
		}
		writeSuccess(options.output, map[string]any{
			"ok": true, "command": "config path", "path": path, "source": source,
		})
		return exitOK
	case "validate":
		path, err := configForValidation(options)
		if err != nil {
			writeFailure(options.output, exitConfigNotFound, "config_not_found", "Файл конфигурации не найден.")
			return exitConfigNotFound
		}
		if err := validation.Validate(path); err != nil {
			code := "config_invalid"
			if validation.IsUnknownField(err) {
				code = "unknown_field"
			}
			writeFailure(options.output, exitValidation, code, "Конфигурация не прошла проверку.")
			return exitValidation
		}
		writeSuccess(options.output, map[string]any{
			"ok": true, "command": "config validate", "valid": true,
		})
		return exitOK
	default:
		writeFailure(options.output, exitConfigNotFound, "config_not_found", "неизвестная config-команда")
		return exitConfigNotFound
	}
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
		return path, "flag", err
	}
	if path := os.Getenv("LIAPOLDUS_GATEWAY_CONFIG"); path != "" {
		resolved, err := absoluteExistingFile(path)
		return resolved, "environment", err
	}
	if options.configDir != "" {
		path, err := absoluteExistingFile(filepath.Join(options.configDir, "gateway.yaml"))
		return path, "flag-directory", err
	}
	if directory := os.Getenv("LIAPOLDUS_CONFIG_DIR"); directory != "" {
		path, err := absoluteExistingFile(filepath.Join(directory, "gateway.yaml"))
		return path, "environment-directory", err
	}
	path, err := absoluteExistingFile("/etc/liapoldus/gateway.yaml")
	return path, "system", err
}

func absoluteExistingFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return "", errors.New("config not found")
	}
	return filepath.Abs(path)
}

func writeSuccess(output string, value map[string]any) {
	if output == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(value)
		return
	}
	fmt.Println("ok")
}

func writeFailure(output string, exitCode int, code, detail string) {
	if output == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"ok":      false,
			"problem": problem{Code: code, Detail: detail},
		})
		return
	}
	fprintln(os.Stderr, detail)
}

func fprintln(file *os.File, value string) {
	_, _ = fmt.Fprintln(file, value)
}
