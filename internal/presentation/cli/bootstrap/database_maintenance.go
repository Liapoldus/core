package bootstrap

import (
	"context"
	"errors"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func BackupDatabase(ctx context.Context, source, destination string) error {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return errors.New("SQLite backup unavailable")
	}
	return storage.BackupSQLite(ctx, source, destination, databaseOptions(contract), contract.BackupIntoQuery)
}

func RestoreDatabase(ctx context.Context, source, destination string) error {
	contract, err := config.LoadSQLiteContract()
	if err != nil {
		return errors.New("SQLite restore unavailable")
	}
	return storage.RestoreSQLite(ctx, source, destination, databaseOptions(contract))
}

func DatabaseBusy(err error) bool { return errors.Is(err, storage.ErrSQLiteStateBusy) }
