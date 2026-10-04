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
	ConfigurationColumns []string `json:"configurationColumns"`
	Seeded               bool     `json:"seeded"`
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	databasePath := os.Args[1]
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
	configRows, err := database.QueryContext(context.Background(), "PRAGMA table_info(plugin_config_generations)")
	if err != nil {
		panic(err)
	}
	configurationColumns := make([]string, 0)
	for configRows.Next() {
		var sequence, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := configRows.Scan(&sequence, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			panic(err)
		}
		configurationColumns = append(configurationColumns, name)
	}
	if err := configRows.Close(); err != nil {
		panic(err)
	}

	manifest := json.RawMessage(`{"name":"serve-fixture","capabilities":["test.lifecycle"]}`)
	_, err = database.ExecContext(context.Background(), `INSERT INTO plugin_instances
		(id, manifest_json, state) VALUES (?, ?, ?)`, "serve-fixture", []byte(manifest), "configured")
	if err != nil {
		panic(err)
	}
	_, err = database.ExecContext(context.Background(), `INSERT INTO plugin_config_generations
		(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
		VALUES (?, 1, 'active', ?, ?, 1, '2026-09-29T00:00:00Z')`,
		"serve-fixture", []byte(`{}`), "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a")
	if err != nil {
		panic(err)
	}
	// v1 Core registers endpoints only: no launch settings and no binary.
	if err := json.NewEncoder(os.Stdout).Encode(report{Columns: columns, ConfigurationColumns: configurationColumns, Seeded: true}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
