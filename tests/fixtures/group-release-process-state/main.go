package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type report struct {
	CurrentRevision  *string `json:"currentRevision"`
	PreviousRevision *string `json:"previousRevision"`
	OperationState   string  `json:"operationState"`
	JournalState     string  `json:"journalState"`
	PendingCount     int     `json:"pendingCount"`
	PendingOperation string  `json:"pendingOperation"`
	CaddyfilePath    string  `json:"caddyfilePath"`
	ArtifactPath     string  `json:"artifactPath"`
	CaddyfileExists  bool    `json:"caddyfileExists"`
	ArtifactExists   bool    `json:"artifactExists"`
}

func main() {
	if len(os.Args) < 4 {
		fatal(fmt.Errorf("expected seed or inspect arguments"))
	}
	ctx := context.Background()
	sqlite, err := config.LoadSQLiteContract()
	check(err)
	database := openDatabase(ctx, os.Args[2], sqlite)
	defer database.Close()
	groups, err := storage.NewSQLiteGroupStore(database)
	check(err)
	switch os.Args[1] {
	case "seed":
		if len(os.Args) != 5 {
			fatal(fmt.Errorf("seed requires database, artifact root, and public address"))
		}
		previous, current, seedErr := seed(ctx, groups, os.Args[3], os.Args[4])
		check(seedErr)
		check(json.NewEncoder(os.Stdout).Encode(map[string]string{
			"currentRevision": current.ID, "previousRevision": previous.ID,
		}))
	case "inspect":
		if len(os.Args) < 5 || len(os.Args) > 7 {
			fatal(fmt.Errorf("inspect requires database, artifact root, operation id, and optional staged paths"))
		}
		value := inspect(ctx, database, groups, os.Args[3], os.Args[4], optional(os.Args, 5), optional(os.Args, 6))
		check(json.NewEncoder(os.Stdout).Encode(value))
	default:
		fatal(fmt.Errorf("unknown action"))
	}
}

func seed(ctx context.Context, groups *storage.SQLiteGroupStore, root, address string) (models.GroupRevision, models.GroupRevision, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return models.GroupRevision{}, models.GroupRevision{}, err
	}
	previous := caddyfile(address, "previous-release")
	current := caddyfile(address, "current-release")
	previousRevision, err := createRevision(ctx, groups, root, strings.Repeat("a", 64), previous)
	if err != nil {
		return models.GroupRevision{}, models.GroupRevision{}, err
	}
	if _, err := groups.AdvanceCurrent(ctx, "system", previousRevision.ID, nil); err != nil {
		return models.GroupRevision{}, models.GroupRevision{}, err
	}
	currentRevision, err := createRevision(ctx, groups, root, strings.Repeat("b", 64), current)
	if err != nil {
		return models.GroupRevision{}, models.GroupRevision{}, err
	}
	_, err = groups.AdvanceCurrent(ctx, "system", currentRevision.ID, &previousRevision.ID)
	return previousRevision, currentRevision, err
}

func inspect(ctx context.Context, database *sql.DB, groups *storage.SQLiteGroupStore, root, operationID, stagedCaddyfile, stagedArtifact string) report {
	pointers, err := groups.GetPointers(ctx, "system")
	check(err)
	releases, err := storage.NewSQLiteGroupReleaseStore(database)
	check(err)
	pending, err := releases.Pending(ctx)
	check(err)
	operations, err := storage.NewSQLiteOperationStore(database)
	check(err)
	operation, err := operations.Get(ctx, operationID)
	check(err)
	var journalState string
	check(database.QueryRowContext(ctx, "SELECT state FROM group_release_journal WHERE operation_id = ?", operationID).Scan(&journalState))
	value := report{
		CurrentRevision: pointers.CurrentRevisionID, PreviousRevision: pointers.PreviousRevisionID,
		OperationState: operation.State, JournalState: journalState, PendingCount: len(pending),
		CaddyfilePath: stagedCaddyfile, ArtifactPath: stagedArtifact,
	}
	for _, reservation := range pending {
		if reservation.OperationID != operationID {
			continue
		}
		value.PendingOperation = reservation.OperationID
		value.CaddyfilePath = reservation.CaddyfilePath
		if reservation.ArtifactPath != nil {
			value.ArtifactPath = *reservation.ArtifactPath
		}
	}
	value.CaddyfileExists = exists(root, value.CaddyfilePath)
	value.ArtifactExists = exists(root, value.ArtifactPath)
	return value
}

func createRevision(ctx context.Context, store *storage.SQLiteGroupStore, root, id string, caddyfile []byte) (models.GroupRevision, error) {
	directory := filepath.Join(root, "releases", id)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return models.GroupRevision{}, err
	}
	path := filepath.Join("releases", id, "Caddyfile")
	if err := os.WriteFile(filepath.Join(root, path), caddyfile, 0o600); err != nil {
		return models.GroupRevision{}, err
	}
	digest := sha256.Sum256(caddyfile)
	return store.CreateRevision(ctx, models.GroupRevision{
		ID: id, GroupID: "system", CaddyfileDigest: hex.EncodeToString(digest[:]),
		CaddyfilePath: path, Actor: "process-crash-fixture",
	})
}

func caddyfile(address, response string) []byte {
	return []byte(fmt.Sprintf("http://%s {\n  respond %q\n}\n", address, response))
}

func exists(root, path string) bool {
	if path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	_, err := os.Stat(path)
	return err == nil
}

func optional(arguments []string, index int) string {
	if len(arguments) <= index {
		return ""
	}
	return arguments[index]
}

func openDatabase(ctx context.Context, path string, contract config.SQLiteContract) *sql.DB {
	database, err := storage.OpenSQLite(ctx, path, storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	check(err)
	return database
}

func check(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
