package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type SQLiteOptions struct {
	Driver                 string
	ParentDirectoryMode    uint32
	DatabaseFileMode       uint32
	MaxOpenConnections     int
	MaxIdleConnections     int
	SchemaVersion          int
	HasMigrationTableQuery string
	MigrationVersionQuery  string
	SchemaVersionError     string
	IntegrityCheckQuery    string
	ForeignKeyCheckQuery   string
	IntegritySuccess       string
	IntegrityError         string
	BackupIntoQuery        string
	Pragmas                string
}

func OpenSQLite(ctx context.Context, path string, options SQLiteOptions, schema []byte) (*sql.DB, error) {
	if options.SchemaVersion <= 0 || options.HasMigrationTableQuery == "" || options.MigrationVersionQuery == "" || options.SchemaVersionError == "" {
		return nil, errors.New(options.SchemaVersionError)
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), os.FileMode(options.ParentDirectoryMode)); err != nil {
		return nil, err
	}
	databaseFile, err := os.OpenFile(absolutePath, os.O_CREATE|os.O_RDWR, os.FileMode(options.DatabaseFileMode))
	if err != nil {
		return nil, err
	}
	if err := databaseFile.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(absolutePath, os.FileMode(options.DatabaseFileMode)); err != nil {
		return nil, err
	}
	database, err := sql.Open(options.Driver, absolutePath)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(options.MaxOpenConnections)
	database.SetMaxIdleConns(options.MaxIdleConnections)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	var hasMigrationTable bool
	if err := database.QueryRowContext(ctx, options.HasMigrationTableQuery).Scan(&hasMigrationTable); err != nil {
		_ = database.Close()
		return nil, errors.New(options.SchemaVersionError)
	}
	if hasMigrationTable {
		var schemaVersion int
		if err := database.QueryRowContext(ctx, options.MigrationVersionQuery).Scan(&schemaVersion); err != nil || schemaVersion < 0 || schemaVersion > options.SchemaVersion {
			_ = database.Close()
			return nil, errors.New(options.SchemaVersionError)
		}
	}
	if _, err := database.ExecContext(ctx, options.Pragmas); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := verifySQLiteIntegrity(ctx, database, options); err != nil {
		_ = database.Close()
		return nil, err
	}
	if _, err := database.ExecContext(ctx, string(schema)); err != nil {
		_ = database.Close()
		return nil, err
	}
	var resultingSchemaVersion int
	if err := database.QueryRowContext(ctx, options.MigrationVersionQuery).Scan(&resultingSchemaVersion); err != nil || resultingSchemaVersion != options.SchemaVersion {
		_ = database.Close()
		return nil, errors.New(options.SchemaVersionError)
	}
	if err := verifySQLiteIntegrity(ctx, database, options); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func verifySQLiteIntegrity(ctx context.Context, database *sql.DB, options SQLiteOptions) error {
	if options.IntegrityCheckQuery == "" && options.ForeignKeyCheckQuery == "" && options.IntegritySuccess == "" && options.IntegrityError == "" {
		return nil
	}
	if options.IntegrityCheckQuery == "" || options.ForeignKeyCheckQuery == "" || options.IntegritySuccess == "" || options.IntegrityError == "" {
		return errors.New(options.SchemaVersionError)
	}
	var result string
	if err := database.QueryRowContext(ctx, options.IntegrityCheckQuery).Scan(&result); err != nil || result != options.IntegritySuccess {
		return errors.New(options.IntegrityError)
	}
	rows, err := database.QueryContext(ctx, options.ForeignKeyCheckQuery)
	if err != nil {
		return errors.New(options.IntegrityError)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		return errors.New(options.IntegrityError)
	}
	return nil
}
