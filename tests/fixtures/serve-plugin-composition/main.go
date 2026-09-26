package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func main() {
	if len(os.Args) != 5 {
		os.Exit(2)
	}
	databasePath, artifactsPath, pluginBinary, secretPath := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(context.Background(), databasePath, storage.SQLiteOptions{
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

	publicAddress := os.Getenv("LIAPOLDUS_TEST_PUBLIC_ADDRESS")
	caddyfile := []byte("http://" + publicAddress + " {\n  liapoldus_plugin fixture test.lifecycle call\n}\n")
	if err := os.MkdirAll(artifactsPath, 0o700); err != nil {
		panic(err)
	}
	const revisionID = "serve-plugin-composition-revision"
	caddyfilePath := filepath.Join(artifactsPath, "serve-plugin-composition.Caddyfile")
	if err := os.WriteFile(caddyfilePath, caddyfile, 0o600); err != nil {
		panic(err)
	}
	digest := sha256.Sum256(caddyfile)
	manifest, err := json.Marshal(map[string]any{
		"name": "fixture", "protocolVersion": "liapoldus.plugin.v1",
		"capabilities":          []string{"test.lifecycle"},
		"capabilityDescriptors": []map[string]any{{"capability": "test.lifecycle", "modes": []string{"INVOCATION_MODE_CALL"}}},
	})
	if err != nil {
		panic(err)
	}
	launch, err := json.Marshal(map[string]any{
		"binary": pluginBinary,
	})
	if err != nil {
		panic(err)
	}
	settings, err := json.Marshal(map[string]any{"credential": fmt.Sprintf("file:%s", secretPath)})
	if err != nil {
		panic(err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO group_revisions (id, group_id, caddyfile_digest, caddyfile_path, actor) VALUES (?, 'system', ?, ?, 'test')`, []any{revisionID, hex.EncodeToString(digest[:]), filepath.Base(caddyfilePath)}},
		{`UPDATE group_pointers SET current_revision_id = ? WHERE group_id = 'system'`, []any{revisionID}},
		{`INSERT INTO plugin_instances (id, mode, endpoint, settings_json, manifest_json, state, revision) VALUES ('fixture', 'local', NULL, ?, ?, 'configured', 1)`, []any{settings, manifest}},
		{`INSERT INTO plugin_launch_settings (instance_id, launch_json) VALUES ('fixture', ?)`, []any{launch}},
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(context.Background(), statement.query, statement.args...); err != nil {
			panic(err)
		}
	}
}
