package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type report struct {
	JournalMode    string   `json:"journalMode"`
	ForeignKeys    int      `json:"foreignKeys"`
	MigrationCount int      `json:"migrationCount"`
	Migration      int      `json:"migrationVersion"`
	RequiredTables []string `json:"requiredTables"`
}

func main() {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(context.Background(), os.Args[1], storage.SQLiteOptions{
		Driver:                 contract.Driver,
		ParentDirectoryMode:    contract.ParentDirectoryMode,
		DatabaseFileMode:       contract.DatabaseFileMode,
		MaxOpenConnections:     contract.MaxOpenConnections,
		MaxIdleConnections:     contract.MaxIdleConnections,
		SchemaVersion:          contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery,
		MigrationVersionQuery:  contract.MigrationVersionQuery,
		SchemaVersionError:     contract.SchemaVersionError,
		Pragmas:                contract.Pragmas,
	}, contract.Schema)
	if err != nil {
		panic(err)
	}
	defer database.Close()
	result := report{}
	if err := database.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&result.JournalMode); err != nil {
		panic(err)
	}
	if err := database.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&result.ForeignKeys); err != nil {
		panic(err)
	}
	if err := database.QueryRowContext(context.Background(), "SELECT COUNT(*), COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&result.MigrationCount, &result.Migration); err != nil {
		panic(err)
	}
	rows, err := database.QueryContext(context.Background(), "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			panic(err)
		}
		result.RequiredTables = append(result.RequiredTables, name)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}
