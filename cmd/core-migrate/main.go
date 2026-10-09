// Command core-migrate is the only supported reader for the retired YAML
// bootstrap format. Core itself never invokes this package at startup.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	settingsstore "github.com/Liapoldus/core/internal/infrastructure/storage/settings"
	bootstrapruntime "github.com/Liapoldus/core/internal/presentation/cli/bootstrap"
)

type migrationPlan struct {
	SchemaVersion                int             `json:"schemaVersion"`
	Settings                     config.Settings `json:"settings"`
	IgnoredStaticPluginInstances []string        `json:"ignoredStaticPluginInstances,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fail(err.Error())
	}
}

func run(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet("core-migrate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "legacy YAML bootstrap path")
	dryRun := flags.Bool("dry-run", false, "validate and print the SQLite import plan without writing")
	apply := flags.Bool("apply", false, "backup the legacy SQLite state and import Core settings")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errors.New("invalid core-migrate arguments")
	}
	if flags.NArg() != 0 || *input == "" || (*dryRun == *apply) {
		return errors.New("provide --input and exactly one of --dry-run or --apply")
	}
	plan, bootstrap, raw, err := prepareMigration(filepath.Clean(*input))
	if err != nil {
		return err
	}
	if *dryRun {
		if err := json.NewEncoder(output).Encode(plan); err != nil {
			return errors.New("write migration plan")
		}
		return nil
	}
	backup, err := importSettings(context.Background(), bootstrap.StatePath, raw)
	if err != nil {
		return err
	}
	result := map[string]any{
		"ok":                           true,
		"database":                     bootstrap.StatePath,
		"settingsRevision":             1,
		"pendingStartupValidation":     true,
		"ignoredStaticPluginInstances": plan.IgnoredStaticPluginInstances,
	}
	if backup != "" {
		result["backup"] = backup
	}
	if err := json.NewEncoder(output).Encode(result); err != nil {
		return errors.New("write migration result")
	}
	return nil
}

func prepareMigration(input string) (migrationPlan, config.BootstrapConfig, []byte, error) {
	legacy, err := config.LoadLegacyBootstrap(input)
	if err != nil {
		return migrationPlan{}, config.BootstrapConfig{}, nil, errors.New("legacy bootstrap validation failed")
	}
	settings := config.Settings{
		Management: config.ManagementSettings{
			Listen: legacy.ManagementListen,
			TLS: config.TLSReferences{
				Certificate: legacy.ManagementCertificate,
				Key:         legacy.ManagementKey,
				ClientCA:    legacy.ManagementClientCA,
			},
			MaxBodyBytes:   legacy.ManagementMaxBodyBytes,
			HeaderTimeout:  legacy.ManagementHeaderTimeout,
			RequestTimeout: legacy.ManagementRequestTimeout,
		},
		PluginControl: config.ControlSettings{
			Listen:            legacy.PluginControlListen,
			PublicURL:         legacy.PluginControlPublicURL,
			TLS:               config.TLSReferences{Certificate: legacy.PluginControlCertificate, Key: legacy.PluginControlKey},
			ReplicaClientCA:   legacy.PluginReplicaClientCA,
			ReplicaServerCA:   legacy.PluginReplicaServerCA,
			ReplicaClientCRLs: append([]string(nil), legacy.PluginReplicaClientCRLs...),
			ReplicaServerCRLs: append([]string(nil), legacy.PluginReplicaServerCRLs...),
		},
		SecretRoot: filepath.Dir(legacy.SourcePath),
	}
	if settings.Management.MaxBodyBytes == 0 {
		settings.Management.MaxBodyBytes = 1 << 20
	}
	if settings.Management.HeaderTimeout == "" {
		settings.Management.HeaderTimeout = "5s"
	}
	if settings.Management.RequestTimeout == "" {
		settings.Management.RequestTimeout = "30s"
	}
	if err := settings.Validate(); err != nil {
		return migrationPlan{}, config.BootstrapConfig{}, nil, errors.New("legacy settings cannot be represented by the current Core settings contract")
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return migrationPlan{}, config.BootstrapConfig{}, nil, errors.New("encode migrated settings")
	}
	ignored := make([]string, 0, len(legacy.Plugins))
	for _, plugin := range legacy.Plugins {
		ignored = append(ignored, plugin.InstanceID)
	}
	return migrationPlan{SchemaVersion: 1, Settings: settings, IgnoredStaticPluginInstances: ignored}, legacy, raw, nil
}

// importSettings holds the same OS lock as Core, backs up the exact SQLite
// contents before schema migration, and restores that snapshot if any import
// step fails. Plugin configuration bytes, generations, audit rows and product
// data are never decoded or rewritten by the migrator.
func importSettings(ctx context.Context, databasePath string, raw []byte) (string, error) {
	if ctx == nil || !filepath.IsAbs(databasePath) || len(raw) == 0 {
		return "", errors.New("invalid offline migration request")
	}
	path := filepath.Clean(databasePath)
	sqlite, err := config.LoadSQLiteContract()
	if err != nil {
		return "", errors.New("SQLite migration contract unavailable")
	}
	unlock, err := storage.AcquireSQLiteStateLock(path, sqlite.ParentDirectoryMode)
	if err != nil {
		return "", errors.New("Core SQLite state is locked or unavailable")
	}
	defer unlock()
	backup, existed, err := backupDatabase(ctx, path, sqlite)
	if err != nil {
		return "", errors.New("could not create and validate pre-migration SQLite backup")
	}
	rollback := func(cause error, database *sql.DB) error {
		if database != nil {
			_ = database.Close()
		}
		if existed {
			if restoreErr := restoreDatabase(backup, path); restoreErr != nil {
				return fmt.Errorf("SQLite import failed; restore the preserved backup %q manually: %w", backup, cause)
			}
			return fmt.Errorf("%w (pre-migration backup retained at %q)", cause, backup)
		} else if removeErr := removeDatabaseFiles(path); removeErr != nil {
			return fmt.Errorf("SQLite import failed and the new database could not be removed: %w", cause)
		}
		return cause
	}

	database, err := bootstrapruntime.OpenDatabase(ctx, path)
	if err != nil {
		return "", rollback(errors.New("Core SQLite schema migration failed"), nil)
	}
	if err := settingsstore.EnsureSchema(database); err != nil {
		return "", rollback(errors.New("Core settings schema initialization failed"), database)
	}
	if _, err := settingsstore.New(database).Init(ctx, raw, "offline-migration"); err != nil {
		return "", rollback(errors.New("Core settings import failed; existing revisions were not replaced"), database)
	}
	if err := database.Close(); err != nil {
		return "", rollback(errors.New("close migrated SQLite database failed"), nil)
	}
	return backup, nil
}

func backupDatabase(ctx context.Context, path string, contract config.SQLiteContract) (string, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", false, errors.New("SQLite state is not a regular file")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".core-pre-migration-*.sqlite")
	if err != nil {
		return "", false, errors.New("reserve SQLite backup path")
	}
	backup := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(backup)
		return "", false, errors.New("reserve SQLite backup path")
	}
	if err := os.Remove(backup); err != nil {
		return "", false, errors.New("reserve SQLite backup path")
	}
	source, err := sql.Open(contract.Driver, path)
	if err != nil {
		return "", false, errors.New("open SQLite source for backup")
	}
	source.SetMaxOpenConns(1)
	source.SetMaxIdleConns(1)
	defer source.Close()
	if err := source.PingContext(ctx); err != nil {
		return "", false, errors.New("open SQLite source for backup")
	}
	if err := verifyIntegrity(ctx, source, contract); err != nil {
		return "", false, err
	}
	if _, err := source.ExecContext(ctx, contract.BackupIntoQuery, backup); err != nil {
		_ = os.Remove(backup)
		return "", false, errors.New("SQLite backup failed")
	}
	if err := os.Chmod(backup, os.FileMode(contract.DatabaseFileMode)); err != nil {
		_ = os.Remove(backup)
		return "", false, errors.New("secure SQLite backup failed")
	}
	backupDB, err := sql.Open(contract.Driver, backup)
	if err != nil {
		_ = os.Remove(backup)
		return "", false, errors.New("validate SQLite backup failed")
	}
	backupDB.SetMaxOpenConns(1)
	backupErr := verifyIntegrity(ctx, backupDB, contract)
	closeErr := backupDB.Close()
	if backupErr != nil || closeErr != nil {
		_ = os.Remove(backup)
		return "", false, errors.New("validate SQLite backup failed")
	}
	backupFile, err := os.OpenFile(backup, os.O_RDWR, os.FileMode(contract.DatabaseFileMode))
	if err != nil {
		_ = os.Remove(backup)
		return "", false, errors.New("secure SQLite backup failed")
	}
	syncErr := backupFile.Sync()
	closeErr = backupFile.Close()
	if syncErr != nil || closeErr != nil {
		_ = os.Remove(backup)
		return "", false, errors.New("secure SQLite backup failed")
	}
	return backup, true, nil
}

func verifyIntegrity(ctx context.Context, database *sql.DB, contract config.SQLiteContract) error {
	var result string
	if err := database.QueryRowContext(ctx, contract.IntegrityCheckQuery).Scan(&result); err != nil || result != contract.IntegritySuccess {
		return errors.New("SQLite integrity check failed")
	}
	rows, err := database.QueryContext(ctx, contract.ForeignKeyCheckQuery)
	if err != nil {
		return errors.New("SQLite foreign-key check failed")
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		return errors.New("SQLite foreign-key check failed")
	}
	return nil
}

func restoreDatabase(backup, path string) error {
	stage, err := os.CreateTemp(filepath.Dir(path), ".core-migration-restore-*.sqlite")
	if err != nil {
		return err
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	source, err := os.Open(backup)
	if err != nil {
		_ = stage.Close()
		return err
	}
	_, copyErr := io.Copy(stage, source)
	closeSourceErr := source.Close()
	if copyErr != nil || closeSourceErr != nil {
		_ = stage.Close()
		return errors.New("copy SQLite recovery snapshot")
	}
	if err := stage.Chmod(0o600); err != nil {
		_ = stage.Close()
		return err
	}
	if err := stage.Sync(); err != nil {
		_ = stage.Close()
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	if err := removeDatabaseSidecars(path); err != nil {
		return err
	}
	return os.Rename(stagePath, path)
}

func removeDatabaseFiles(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return removeDatabaseSidecars(path)
}

func removeDatabaseSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func fail(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
