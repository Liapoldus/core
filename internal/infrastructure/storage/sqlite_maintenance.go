package storage

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
)

var ErrSQLiteStateBusy = errors.New("SQLite state is in use")

func AcquireSQLiteStateLock(path string, mode uint32) (func(), error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("invalid SQLite state path")
	}
	lockPath := absolute + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), os.FileMode(mode)); err != nil {
		return nil, errors.New("unable to prepare SQLite state lock")
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errors.New("unable to open SQLite state lock")
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, errors.New("unable to secure SQLite state lock")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, ErrSQLiteStateBusy
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func BackupSQLite(ctx context.Context, source, destination string, options SQLiteOptions, vacuumInto string) error {
	if ctx == nil || options.Driver == "" || vacuumInto == "" {
		return errors.New("invalid SQLite backup request")
	}
	sourcePath, err := filepath.Abs(source)
	if err != nil {
		return errors.New("invalid SQLite source path")
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil || sourcePath == destinationPath {
		return errors.New("invalid SQLite backup destination")
	}
	if _, err := os.Stat(destinationPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("SQLite backup destination already exists")
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), os.FileMode(options.ParentDirectoryMode)); err != nil {
		return errors.New("unable to prepare SQLite backup directory")
	}
	stage, err := os.CreateTemp(filepath.Dir(destinationPath), ".core-backup-*")
	if err != nil {
		return errors.New("unable to stage SQLite backup")
	}
	stagePath := stage.Name()
	if err := stage.Close(); err != nil {
		_ = os.Remove(stagePath)
		return errors.New("unable to stage SQLite backup")
	}
	if err := os.Remove(stagePath); err != nil {
		return errors.New("unable to stage SQLite backup")
	}
	defer os.Remove(stagePath)

	database, err := sql.Open(options.Driver, sourcePath)
	if err != nil {
		return errors.New("unable to open SQLite state")
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return errors.New("unable to open SQLite state")
	}
	if err := verifySQLiteSnapshot(ctx, database, options, options.SchemaVersion); err != nil {
		return err
	}
	if _, err := database.ExecContext(ctx, vacuumInto, stagePath); err != nil {
		return errors.New("unable to create SQLite backup")
	}
	if err := validateSQLiteFile(ctx, stagePath, options); err != nil {
		return errors.New("SQLite backup validation failed")
	}
	if err := os.Chmod(stagePath, os.FileMode(options.DatabaseFileMode)); err != nil {
		return errors.New("unable to secure SQLite backup")
	}
	if err := syncFile(stagePath); err != nil {
		return errors.New("unable to secure SQLite backup")
	}
	if err := os.Link(stagePath, destinationPath); err != nil {
		return errors.New("unable to publish SQLite backup")
	}
	if err := os.Remove(stagePath); err != nil {
		return errors.New("unable to publish SQLite backup")
	}
	if err := syncDirectory(filepath.Dir(destinationPath)); err != nil {
		return errors.New("unable to persist SQLite backup")
	}
	return nil
}

func RestoreSQLite(ctx context.Context, source, destination string, options SQLiteOptions) error {
	if ctx == nil || options.Driver == "" || options.SchemaVersion <= 0 {
		return errors.New("invalid SQLite restore request")
	}
	sourcePath, err := filepath.Abs(source)
	if err != nil {
		return errors.New("invalid SQLite restore source")
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil || sourcePath == destinationPath {
		return errors.New("invalid SQLite restore destination")
	}
	if err := validateSQLiteFile(ctx, sourcePath, options); err != nil {
		return errors.New("SQLite restore source is invalid")
	}
	unlock, err := AcquireSQLiteStateLock(destinationPath, options.ParentDirectoryMode)
	if err != nil {
		return err
	}
	defer unlock()
	if err := os.MkdirAll(filepath.Dir(destinationPath), os.FileMode(options.ParentDirectoryMode)); err != nil {
		return errors.New("unable to prepare SQLite state directory")
	}
	stage, err := os.CreateTemp(filepath.Dir(destinationPath), ".core-restore-*")
	if err != nil {
		return errors.New("unable to stage SQLite restore")
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	input, err := os.Open(sourcePath)
	if err != nil {
		_ = stage.Close()
		return errors.New("unable to read SQLite restore source")
	}
	_, copyErr := io.Copy(stage, input)
	inputErr := input.Close()
	if copyErr != nil || inputErr != nil || stage.Sync() != nil || stage.Close() != nil {
		return errors.New("unable to stage SQLite restore")
	}
	if err := os.Chmod(stagePath, os.FileMode(options.DatabaseFileMode)); err != nil {
		return errors.New("unable to secure SQLite restore")
	}
	if err := validateSQLiteFile(ctx, stagePath, options); err != nil {
		return errors.New("SQLite restore source is invalid")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(destinationPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("unable to prepare SQLite state for restore")
		}
	}
	if err := os.Rename(stagePath, destinationPath); err != nil {
		return errors.New("unable to activate SQLite restore")
	}
	if err := syncDirectory(filepath.Dir(destinationPath)); err != nil {
		return errors.New("unable to persist SQLite restore")
	}
	return nil
}

func validateSQLiteFile(ctx context.Context, path string, options SQLiteOptions) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return errors.New("invalid SQLite file path")
	}
	uri := (&url.URL{Scheme: "file", Path: absolute}).String() + "?mode=ro"
	database, err := sql.Open(options.Driver, uri)
	if err != nil {
		return errors.New("unable to inspect SQLite file")
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return errors.New("SQLite file is unreadable")
	}
	return verifySQLiteSnapshot(ctx, database, options, options.SchemaVersion)
}

func verifySQLiteSnapshot(ctx context.Context, database *sql.DB, options SQLiteOptions, expectedVersion int) error {
	var version int
	if err := database.QueryRowContext(ctx, options.MigrationVersionQuery).Scan(&version); err != nil || version != expectedVersion {
		return errors.New("SQLite schema version is incompatible")
	}
	var integrity string
	if err := database.QueryRowContext(ctx, options.IntegrityCheckQuery).Scan(&integrity); err != nil || integrity != options.IntegritySuccess {
		return errors.New("SQLite integrity check failed")
	}
	rows, err := database.QueryContext(ctx, options.ForeignKeyCheckQuery)
	if err != nil {
		return errors.New("SQLite foreign-key check failed")
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("SQLite foreign-key check failed")
	}
	if err := rows.Err(); err != nil {
		return errors.New("SQLite foreign-key check failed")
	}
	return nil
}

func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
