package storage

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var ErrSQLiteStateBusy = errors.New("SQLite state is in use")

// AcquireSQLiteStateLock serializes Core startup and shutdown against external
// state maintenance. Backup/restore is deliberately not a Core capability;
// the standalone CLI owns those file-level operations.
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
