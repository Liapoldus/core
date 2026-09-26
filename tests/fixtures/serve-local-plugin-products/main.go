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
	"github.com/Liapoldus/pluginprotocol"
)

type capability struct {
	Name  string   `json:"capability"`
	Modes []string `json:"modes"`
}

type manifest struct {
	Name                  string       `json:"name"`
	ProtocolVersion       string       `json:"protocolVersion"`
	Capabilities          []string     `json:"capabilities"`
	CapabilityDescriptors []capability `json:"capabilityDescriptors"`
}

type pluginSeed struct {
	id       string
	address  string
	binary   string
	settings any
	manifest manifest
}

func main() {
	if len(os.Args) != 9 {
		os.Exit(2)
	}
	databasePath, artifactsPath := os.Args[1], os.Args[2]
	seeds := []pluginSeed{
		{
			id: "captcha", address: os.Args[3], binary: os.Args[6], settings: map[string]any{},
			manifest: pluginManifest("captcha", "captcha.verify"),
		},
		{
			id: "forms-db", address: os.Args[4], binary: os.Args[7],
			settings: map[string]any{
				"driver": "memory",
				"schemas": map[string]any{
					"contact": map[string]any{
						"$schema":              "https://json-schema.org/draft/2020-12/schema",
						"type":                 "object",
						"properties":           map[string]any{"email": map[string]any{"type": "string"}},
						"required":             []string{"email"},
						"additionalProperties": false,
					},
				},
			},
			manifest: pluginManifest("forms-db", "forms.submit"),
		},
		{
			id: "identity", address: os.Args[5], binary: os.Args[8], settings: map[string]any{},
			manifest: pluginManifest("identity", "identity.server.jwks"),
		},
	}

	caddyfile := make([]byte, 0)
	for _, seed := range seeds {
		caddyfile = append(caddyfile, []byte(fmt.Sprintf("http://%s {\n  liapoldus_plugin %s %s call\n}\n", seed.address, seed.id, seed.manifest.Capabilities[0]))...)
	}
	if err := os.MkdirAll(artifactsPath, 0o700); err != nil {
		panic(err)
	}
	caddyfileName := "real-plugin-smoke.Caddyfile"
	caddyfilePath := filepath.Join(artifactsPath, caddyfileName)
	if err := os.WriteFile(caddyfilePath, caddyfile, 0o600); err != nil {
		panic(err)
	}

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

	digest := sha256.Sum256(caddyfile)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO group_revisions (id, group_id, caddyfile_digest, caddyfile_path, actor) VALUES (?, 'system', ?, ?, 'test')`, []any{"real-plugin-smoke-revision", hex.EncodeToString(digest[:]), caddyfileName}},
		{`UPDATE group_pointers SET current_revision_id = ? WHERE group_id = 'system'`, []any{"real-plugin-smoke-revision"}},
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(context.Background(), statement.query, statement.args...); err != nil {
			panic(err)
		}
	}
	for _, seed := range seeds {
		settings, err := json.Marshal(seed.settings)
		if err != nil {
			panic(err)
		}
		manifestJSON, err := json.Marshal(seed.manifest)
		if err != nil {
			panic(err)
		}
		if _, err := database.ExecContext(context.Background(), `INSERT INTO plugin_instances
			(id, mode, endpoint, settings_json, manifest_json, state, revision)
			VALUES (?, 'local', NULL, ?, ?, 'configured', 1)`, seed.id, settings, manifestJSON); err != nil {
			panic(err)
		}
		launch, err := json.Marshal(map[string]string{"binary": seed.binary})
		if err != nil {
			panic(err)
		}
		if _, err := database.ExecContext(context.Background(), `INSERT INTO plugin_launch_settings (instance_id, launch_json) VALUES (?, ?)`, seed.id, launch); err != nil {
			panic(err)
		}
	}
}

func pluginManifest(name, capabilityName string) manifest {
	return manifest{
		Name: name, ProtocolVersion: pluginprotocol.ProtocolVersion,
		Capabilities: []string{capabilityName},
		CapabilityDescriptors: []capability{{
			Name:  capabilityName,
			Modes: []string{"INVOCATION_MODE_CALL"},
		}},
	}
}
