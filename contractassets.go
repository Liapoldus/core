// Package core exposes embedded static Core contracts to infrastructure.
package core

import (
	"embed"
	"fmt"
)

const (
	ConfigFields            = "config-fields.yaml"
	CoreSchema              = "core.schema.json"
	ManagementFields        = "management-fields.yaml"
	AuditFields             = "audit-fields.yaml"
	ErrorsJSON              = "errors.json"
	SQLiteRuntime           = "sqlite-runtime.yaml"
	SQLitePluginInstances   = "sqlite-plugin-instances.yaml"
	PluginSecretGrants      = "plugin-secret-grants.yaml"
	PluginReconciliation    = "plugin-reconciliation.yaml"
	TrafficRolloutFields    = "v2/traffic-rollout-fields.yaml"
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

//go:embed contracts/v2
var v2Contracts embed.FS

func ContractV2(name string) ([]byte, error) {
	contents, err := v2Contracts.ReadFile("contracts/v2/" + name)
	if err != nil {
		return nil, fmt.Errorf("read v2 contract %s: %w", name, err)
	}
	return contents, nil
}
