package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type report struct {
	Columns              []string `json:"columns"`
	LaunchSettingsSeeded bool     `json:"launchSettingsSeeded"`
	Seeded               bool     `json:"seeded"`
}

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	databasePath, pluginBinary := os.Args[1], os.Args[2]
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

	columns := make([]string, 0)
	rows, err := database.QueryContext(context.Background(), "PRAGMA table_info(plugin_instances)")
	if err != nil {
		panic(err)
	}
	for rows.Next() {
		var sequence, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&sequence, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			panic(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Close(); err != nil {
		panic(err)
	}

	manifest := json.RawMessage(`{"name":"serve-fixture","protocolVersion":"liapoldus.plugin.v1","capabilities":["test.lifecycle"],"capabilityDescriptors":[{"capability":"test.lifecycle","modes":["INVOCATION_MODE_CALL"]}]}`)
	_, err = database.ExecContext(context.Background(), `INSERT INTO plugin_instances
		(id, mode, endpoint, settings_json, manifest_json, state, revision)
		VALUES (?, ?, ?, ?, ?, ?, 1)`,
		"serve-fixture", "local", nil, []byte(`{}`), []byte(manifest), "configured")
	if err != nil {
		panic(err)
	}
	launch, err := json.Marshal(map[string]string{"binary": pluginBinary})
	if err != nil {
		panic(err)
	}
	if _, err := database.ExecContext(context.Background(), `INSERT INTO plugin_launch_settings (instance_id, launch_json) VALUES (?, ?)`, "serve-fixture", launch); err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(report{Columns: columns, LaunchSettingsSeeded: true, Seeded: true}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
