package runtime

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
	return storage.OpenSQLite(ctx, path, databaseOptions(contract), contract.Schema)
}

func OpenServingDatabase(ctx context.Context, path string) (*sql.DB, func(), error) {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return nil, nil, err
	}
	unlock, err := storage.AcquireSQLiteStateLock(path, contract.ParentDirectoryMode)
	if err != nil {
		return nil, nil, err
	}
	database, err := storage.OpenSQLite(ctx, path, databaseOptions(contract), contract.Schema)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return database, unlock, nil
}

func OpenExclusiveDatabase(ctx context.Context, path string) (*sql.DB, func(), error) {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return nil, nil, err
	}
	unlock, err := storage.AcquireSQLiteStateLock(path, contract.ParentDirectoryMode)
	if err != nil {
		return nil, nil, err
	}
	database, err := storage.OpenSQLite(ctx, path, databaseOptions(contract), contract.Schema)
	if err != nil {
		unlock()
		return nil, nil, err
	}
	return database, unlock, nil
}

func databaseOptions(contract config.SQLiteContract) storage.SQLiteOptions {
	return storage.SQLiteOptions{
		Driver:                 contract.Driver,
		ParentDirectoryMode:    contract.ParentDirectoryMode,
		DatabaseFileMode:       contract.DatabaseFileMode,
		MaxOpenConnections:     contract.MaxOpenConnections,
		MaxIdleConnections:     contract.MaxIdleConnections,
		SchemaVersion:          contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery,
		MigrationVersionQuery:  contract.MigrationVersionQuery,
		SchemaVersionError:     contract.SchemaVersionError,
		IntegrityCheckQuery:    contract.IntegrityCheckQuery,
		ForeignKeyCheckQuery:   contract.ForeignKeyCheckQuery,
		IntegritySuccess:       contract.IntegritySuccess,
		IntegrityError:         contract.IntegrityError,
		BackupIntoQuery:        contract.BackupIntoQuery,
		Pragmas:                contract.Pragmas,
	}
}
