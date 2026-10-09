package cli

import (
	"context"
	"path/filepath"

	bootstrapruntime "github.com/Liapoldus/core/internal/presentation/cli/bootstrap"
)

func database(options options) int {
	if len(options.command) != 3 || options.command[2] == "" {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.DatabaseCommandUsage, words.Diagnostics.DatabaseCommandUsage)
		return words.Exits.Arguments
	}
	statePath, err := statePath()
	if err != nil {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
		return words.Exits.Arguments
	}
	path, err := filepath.Abs(options.command[2])
	if err != nil {
		writeDatabaseFailure(options, options.command[1], nil)
		return words.Exits.Unavailable
	}
	switch options.command[1] {
	case words.Database.Backup:
		err = bootstrapruntime.BackupDatabase(context.Background(), statePath, path)
	case words.Database.Restore:
		err = bootstrapruntime.RestoreDatabase(context.Background(), path, statePath)
	default:
		writeFailure(options.output, words.Exits.Arguments, words.Codes.DatabaseCommandUsage, words.Diagnostics.DatabaseCommandUsage)
		return words.Exits.Arguments
	}
	if err != nil {
		writeDatabaseFailure(options, options.command[1], err)
		if bootstrapruntime.DatabaseBusy(err) {
			return words.Exits.Conflict
		}
		return words.Exits.Unavailable
	}
	writeSuccess(options.output, map[string]any{words.JSON.OK: true})
	return words.Exits.OK
}

func writeDatabaseFailure(options options, operation string, err error) {
	if bootstrapruntime.DatabaseBusy(err) {
		writeFailure(options.output, words.Exits.Conflict, words.Codes.DatabaseBusy, words.Diagnostics.DatabaseBusy)
		return
	}
	if operation == words.Database.Backup {
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.DatabaseBackupFailed, words.Diagnostics.DatabaseBackupFailed)
		return
	}
	writeFailure(options.output, words.Exits.Unavailable, words.Codes.DatabaseRestoreFailed, words.Diagnostics.DatabaseRestoreFailed)
}
