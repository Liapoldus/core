// Package core exposes embedded static Gateway contracts to infrastructure.
package core

import (
	"embed"
	"fmt"
)

const (
	ConfigFields            = "config-fields.yaml"
	CLIFields               = "cli-fields.yaml"
	GatewaySchema           = "gateway.schema.json"
	ManagementFields        = "management-fields.yaml"
	AuditFields             = "audit-fields.yaml"
	ErrorsJSON              = "errors.json"
	SQLiteRuntime           = "sqlite-runtime.yaml"
	SQLiteSchema            = "sqlite-schema.sql"
	SQLiteGroupStore        = "sqlite-group-store.yaml"
	SQLiteAccess            = "sqlite-access.yaml"
	SQLiteAuditStore        = "sqlite-audit-store.yaml"
	SQLiteOperationStore    = "sqlite-operation-store.yaml"
	SQLiteGroupReleaseStore = "sqlite-group-release-store.yaml"
	SQLitePluginInstances   = "sqlite-plugin-instances.yaml"
	PluginLocalLaunchSchema = "local-launch.schema.json"
	PluginRuntime           = "plugin-runtime.json"
	CaddyHTTPStream         = "caddy-http-stream.json"
	GroupReleaseArtifacts   = "group-release-artifacts.yaml"
	GroupReleaseWorkflow    = "group-release-workflow.yaml"
	CaddyBuild              = "caddy-build.json"
	CaddyPlugin             = "caddy-plugin.json"
	CaddyExternal           = "caddy-external.json"
	CaddyL4Plugin           = "caddy-l4-plugin.json"
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
