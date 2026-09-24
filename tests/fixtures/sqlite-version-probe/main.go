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

func main() {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		writeReport(false)
		return
	}
	database, err := sql.Open(contract.Driver, os.Args[1])
	if err != nil {
		writeReport(false)
		return
	}
	if _, err := database.ExecContext(context.Background(), "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)"); err != nil {
		_ = database.Close()
		writeReport(false)
		return
	}
	if _, err := database.ExecContext(context.Background(), "INSERT INTO schema_migrations(version) VALUES (2)"); err != nil {
		_ = database.Close()
		writeReport(false)
		return
	}
	if err := database.Close(); err != nil {
		writeReport(false)
		return
	}
	database, err = storage.OpenSQLite(context.Background(), os.Args[1], storage.SQLiteOptions{
		Driver:              contract.Driver,
		ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode:    contract.DatabaseFileMode,
		MaxOpenConnections:  contract.MaxOpenConnections,
		MaxIdleConnections:  contract.MaxIdleConnections,
		Pragmas:             contract.Pragmas,
	}, contract.Schema)
	if database != nil {
		_ = database.Close()
	}
	writeReport(err != nil)
}

func writeReport(rejected bool) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]bool{"rejected": rejected})
}
