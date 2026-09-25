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
	Columns []string `json:"columns"`
	Seeded  bool     `json:"seeded"`
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(context.Background(), os.Args[1], storage.SQLiteOptions{
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

	manifest := json.RawMessage(`{"name":"serve-fixture","protocolVersion":"liapoldus.plugin.v1","capabilities":["forms.submit","admin.surface.get"],"capabilityDescriptors":[{"capability":"forms.submit","modes":["INVOCATION_MODE_CALL"]},{"capability":"admin.surface.get","modes":["INVOCATION_MODE_CALL"]}]}`)
	_, err = database.ExecContext(context.Background(), `INSERT INTO plugin_instances
		(id, mode, endpoint, settings_json, manifest_json, state, revision)
		VALUES (?, ?, NULL, ?, ?, ?, 1)`,
		"serve-fixture", "local", []byte(`{}`), []byte(manifest), "configured")
	if err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(report{Columns: columns, Seeded: true}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
