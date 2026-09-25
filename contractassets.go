// Package core exposes embedded static Gateway contracts to infrastructure.
package core

import (
	"embed"
	"fmt"
)

const (
	ConfigFields     = "config-fields.yaml"
	CLIFields        = "cli-fields.yaml"
	GatewaySchema    = "gateway.schema.json"
	ManagementFields = "management-fields.yaml"
	AuditFields      = "audit-fields.yaml"
	ErrorsJSON       = "errors.json"
	SQLiteRuntime    = "sqlite-runtime.yaml"
	SQLiteSchema     = "sqlite-schema.sql"
	SQLiteGroupStore = "sqlite-group-store.yaml"
	SQLiteAccess     = "sqlite-access.yaml"
	SQLiteAuditStore = "sqlite-audit-store.yaml"
	CaddyBuild       = "caddy-build.json"
	CaddyPlugin      = "caddy-plugin.json"
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
