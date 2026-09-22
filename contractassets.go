// Package core exposes embedded static Gateway contracts to infrastructure.
package core

import (
	"embed"
	"fmt"
)

const (
	ConfigFields       = "config-fields.yaml"
	CLIFields          = "cli-fields.yaml"
	RegistryFields     = "registry-fields.yaml"
	SnapshotFields     = "snapshot-fields.yaml"
	GatewaySchema      = "gateway.schema.json"
	ManagementFields   = "management-fields.yaml"
	ObservabilityFields = "observability-fields.yaml"
	ErrorsJSON         = "errors.json"
)

//go:embed assets/contracts
var contracts embed.FS

func Contract(name string) ([]byte, error) {
	contents, err := contracts.ReadFile("assets/contracts/" + name)
	if err != nil {
		return nil, fmt.Errorf("read contract %s: %w", name, err)
	}
	return contents, nil
}
