// Package assets exposes versioned external contract files compiled into the
// binary with go:embed. Contract vocabulary must stay out of Go source; these
// files are the single source of those strings.
package assets

import (
	"embed"
	"fmt"
)

const (
	ConfigFields   = "config-fields.yaml"
	CLIFields      = "cli-fields.yaml"
	RegistryFields = "registry-fields.yaml"
	GatewaySchema  = "gateway.schema.json"
)

//go:embed contracts
var contracts embed.FS

func Contract(name string) ([]byte, error) {
	contents, err := contracts.ReadFile("contracts/" + name)
	if err != nil {
		return nil, fmt.Errorf("read contract %s: %w", name, err)
	}
	return contents, nil
}