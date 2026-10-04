// Package core exposes embedded static Core contracts to infrastructure.
package core

import (
	"embed"
	"fmt"
)

const (
	ConfigFields              = "config-fields.yaml"
	CLIFields                 = "cli-fields.yaml"
	CoreSchema                = "core.schema.json"
	ManagementFields          = "management-fields.yaml"
	AuditFields               = "audit-fields.yaml"
	ErrorsJSON                = "errors.json"
	SQLiteRuntime             = "sqlite-runtime.yaml"
	SQLiteSchema              = "sqlite-schema.sql"
	SQLiteAccess              = "sqlite-access.yaml"
	SQLiteAuditStore          = "sqlite-audit-store.yaml"
	SQLiteOperationStore      = "sqlite-operation-store.yaml"
	SQLitePluginInstances     = "sqlite-plugin-instances.yaml"
	SQLitePluginConfiguration = "sqlite-plugin-configuration.yaml"
	PluginSecretGrants        = "plugin-secret-grants.yaml"
	PluginReconciliation      = "plugin-reconciliation.yaml"
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
