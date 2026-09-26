package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	_ "modernc.org/sqlite"
)

type report struct {
	Rejected      bool `json:"rejected"`
	ContractError bool `json:"contractError"`
}

func main() {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		writeReport(false, false)
		return
	}
	database, err := sql.Open(contract.Driver, os.Args[1])
	if err != nil {
		writeReport(false, false)
		return
	}
	if _, err := database.ExecContext(context.Background(), "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)"); err != nil {
		_ = database.Close()
		writeReport(false, false)
		return
	}
	if _, err := database.ExecContext(context.Background(), "INSERT INTO schema_migrations(version) VALUES (3)"); err != nil {
		_ = database.Close()
		writeReport(false, false)
		return
	}
	if err := database.Close(); err != nil {
		writeReport(false, false)
		return
	}
	database, err = storage.OpenSQLite(context.Background(), os.Args[1], storage.SQLiteOptions{
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
	if database != nil {
		_ = database.Close()
	}
	writeReport(err != nil, err != nil && err.Error() == contract.SchemaVersionError)
}

func writeReport(rejected, contractError bool) {
	_ = json.NewEncoder(os.Stdout).Encode(report{Rejected: rejected, ContractError: contractError})
}
