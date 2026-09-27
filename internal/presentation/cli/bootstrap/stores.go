package bootstrap

import (
	"context"
	"database/sql"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func OpenDatabase(ctx context.Context, path string) (*sql.DB, error) {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return nil, err
	}
	return storage.OpenSQLite(ctx, path, storage.SQLiteOptions{
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
}
