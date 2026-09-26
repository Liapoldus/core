package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func main() {
	if len(os.Args) != 5 {
		os.Exit(2)
	}
	ctx := context.Background()
	databasePath, artifactsPath, pluginBinary, publicAddress := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(ctx, databasePath, storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	if err != nil {
		panic(err)
	}
	defer database.Close()
	if err := os.MkdirAll(artifactsPath, 0o700); err != nil {
		panic(err)
	}
	caddyfile := []byte("http://" + publicAddress + " {\n  liapoldus_plugin cookie-fixture test.cookie-boundary call\n}\n")
	caddyfilePath := filepath.Join(artifactsPath, "cookie-policy.Caddyfile")
	if err := os.WriteFile(caddyfilePath, caddyfile, 0o600); err != nil {
		panic(err)
	}
	caddyfileDigest := sha256.Sum256(caddyfile)
	manifest := json.RawMessage(`{"name":"cookie-fixture","protocolVersion":"liapoldus.plugin.v1","capabilities":["test.cookie-boundary"],"capabilityDescriptors":[{"capability":"test.cookie-boundary","modes":["INVOCATION_MODE_CALL"]}]}`)
	launch, err := json.Marshal(map[string]string{"binary": pluginBinary})
	if err != nil {
		panic(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO group_revisions (id, group_id, caddyfile_digest, caddyfile_path, actor) VALUES (?, 'system', ?, ?, 'fixture')`, []any{"c00c1e-policy-revision", hex.EncodeToString(caddyfileDigest[:]), filepath.Base(caddyfilePath)}},
		{`UPDATE group_pointers SET current_revision_id = ? WHERE group_id = 'system'`, []any{"c00c1e-policy-revision"}},
		{`INSERT INTO plugin_instances (id, mode, endpoint, settings_json, manifest_json, state, revision) VALUES (?, ?, NULL, ?, ?, ?, 1)`, []any{"cookie-fixture", "local", []byte(`{}`), []byte(manifest), "configured"}},
		{`INSERT INTO plugin_launch_settings (instance_id, launch_json) VALUES (?, ?)`, []any{"cookie-fixture", launch}},
		{`INSERT INTO plugin_cookie_policies (instance_id, capability, allowed_names_json, revision, updated_at) VALUES (?, ?, ?, ?, ?)`, []any{"cookie-fixture", "test.cookie-boundary", []byte(`["session"]`), int64(7), "2026-09-27T00:00:00Z"}},
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement.query, statement.args...); err != nil {
			panic(err)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]bool{"activeRevisionSeeded": true, "cookiePolicySeeded": true}); err != nil {
		panic(err)
	}
}
