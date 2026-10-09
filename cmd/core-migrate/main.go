// Command core-migrate is the only supported reader for the retired YAML
// bootstrap format. Core itself never invokes this package at startup.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/config"
)

type migrationDocument struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Bootstrap     config.BootstrapConfig `json:"bootstrap"`
}

func main() {
	input := flag.String("input", "", "legacy YAML bootstrap path")
	output := flag.String("output", "", "JSON settings output path")
	dryRun := flag.Bool("dry-run", false, "validate and print the converted document without writing")
	flag.Parse()
	if *input == "" || (*output == "" && !*dryRun) {
		fail("--input is required; --output is required unless --dry-run is used")
	}
	legacy, err := config.LoadBootstrap(filepath.Clean(*input))
	if err != nil {
		fail(fmt.Sprintf("legacy bootstrap validation failed: %v", err))
	}
	document, err := json.MarshalIndent(migrationDocument{SchemaVersion: 1, Bootstrap: legacy}, "", "  ")
	if err != nil {
		fail(fmt.Sprintf("marshal migration document: %v", err))
	}
	document = append(document, '\n')
	if *dryRun {
		_, _ = os.Stdout.Write(document)
		return
	}
	if err := writeWithBackup(filepath.Clean(*output), document); err != nil {
		fail(err.Error())
	}
}

func writeWithBackup(path string, contents []byte) error {
	if path == "" || filepath.IsAbs(path) == false {
		return errors.New("migration output must be an absolute path")
	}
	if _, err := os.Stat(path); err == nil {
		backup := fmt.Sprintf("%s.%s.bak", path, time.Now().UTC().Format("20060102T150405Z"))
		if err := os.Rename(path, backup); err != nil {
			return fmt.Errorf("backup existing output: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect migration output: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create migration output directory: %w", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write migration output: %w", err)
	}
	return nil
}

func fail(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
