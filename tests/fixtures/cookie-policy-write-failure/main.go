package main

import (
	"context"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		os.Exit(1)
	}
	database, err := storage.OpenSQLite(context.Background(), os.Args[1], storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	if err != nil {
		os.Exit(1)
	}
	defer database.Close()
	if _, err := database.ExecContext(context.Background(), `CREATE TRIGGER reject_cookie_policy_update BEFORE UPDATE ON plugin_cookie_policies BEGIN SELECT RAISE(ABORT, 'fixture write failure'); END`); err != nil {
		os.Exit(1)
	}
}
