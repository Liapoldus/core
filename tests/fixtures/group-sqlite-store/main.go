package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type report struct {
	FirstCurrent                       string `json:"firstCurrent"`
	SecondCurrent                      string `json:"secondCurrent"`
	SecondPrevious                     string `json:"secondPrevious"`
	StaleCompareAndSwapRejected        bool   `json:"staleCompareAndSwapRejected"`
	StaleCompareAndSwapUnchangedPointer bool   `json:"staleCompareAndSwapUnchangedPointers"`
	CrossGroupRevisionRejected         bool   `json:"crossGroupRevisionRejected"`
	MissingRevisionRejected            bool   `json:"missingRevisionRejected"`
	ReopenedCurrent                    string `json:"reopenedCurrent"`
	ReopenedPrevious                   string `json:"reopenedPrevious"`
}

func main() {
	ctx := context.Background()
	sqliteContract, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqliteContract.Driver, ParentDirectoryMode: sqliteContract.ParentDirectoryMode,
		DatabaseFileMode: sqliteContract.DatabaseFileMode, MaxOpenConnections: sqliteContract.MaxOpenConnections,
		MaxIdleConnections: sqliteContract.MaxIdleConnections, SchemaVersion: sqliteContract.SchemaVersion,
		HasMigrationTableQuery: sqliteContract.HasMigrationTableQuery, MigrationVersionQuery: sqliteContract.MigrationVersionQuery,
		SchemaVersionError: sqliteContract.SchemaVersionError, Pragmas: sqliteContract.Pragmas,
	}, sqliteContract.Schema)
	if err != nil {
		panic(err)
	}
	store, err := storage.NewSQLiteGroupStore(database, config.LoadGroupStoreContract())
	if err != nil {
		panic(err)
	}
	if _, err := store.CreateApplicationGroup(ctx, "group-one"); err != nil {
		panic(err)
	}
	if _, err := store.CreateApplicationGroup(ctx, "group-two"); err != nil {
		panic(err)
	}
	for _, revision := range []models.GroupRevision{
		{ID: "revision-one", GroupID: "group-one", CaddyfileDigest: "digest-one", CaddyfilePath: "one.caddyfile", Actor: "test"},
		{ID: "revision-two", GroupID: "group-one", CaddyfileDigest: "digest-two", CaddyfilePath: "two.caddyfile", Actor: "test"},
	} {
		if _, err := store.CreateRevision(ctx, revision); err != nil {
			panic(err)
		}
	}
	first, err := store.AdvanceCurrent(ctx, "group-one", "revision-one", nil)
	if err != nil {
		panic(err)
	}
	second, err := store.AdvanceCurrent(ctx, "group-one", "revision-two", first.CurrentRevisionID)
	if err != nil {
		panic(err)
	}
	_, staleErr := store.AdvanceCurrent(ctx, "group-one", "revision-one", nil)
	stale, err := store.GetPointers(ctx, "group-one")
	if err != nil {
		panic(err)
	}
	_, crossGroupErr := store.AdvanceCurrent(ctx, "group-two", "revision-one", nil)
	_, missingRevisionErr := store.AdvanceCurrent(ctx, "group-one", "missing-revision", second.CurrentRevisionID)
	if err := database.Close(); err != nil {
		panic(err)
	}
	database, err = storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqliteContract.Driver, ParentDirectoryMode: sqliteContract.ParentDirectoryMode,
		DatabaseFileMode: sqliteContract.DatabaseFileMode, MaxOpenConnections: sqliteContract.MaxOpenConnections,
		MaxIdleConnections: sqliteContract.MaxIdleConnections, SchemaVersion: sqliteContract.SchemaVersion,
		HasMigrationTableQuery: sqliteContract.HasMigrationTableQuery, MigrationVersionQuery: sqliteContract.MigrationVersionQuery,
		SchemaVersionError: sqliteContract.SchemaVersionError, Pragmas: sqliteContract.Pragmas,
	}, sqliteContract.Schema)
	if err != nil {
		panic(err)
	}
	defer database.Close()
	store, err = storage.NewSQLiteGroupStore(database, config.LoadGroupStoreContract())
	if err != nil {
		panic(err)
	}
	reopened, err := store.GetPointers(ctx, "group-one")
	if err != nil {
		panic(err)
	}
	output := report{
		FirstCurrent: *first.CurrentRevisionID, SecondCurrent: *second.CurrentRevisionID,
		SecondPrevious: *second.PreviousRevisionID, StaleCompareAndSwapRejected: staleErr != nil,
		StaleCompareAndSwapUnchangedPointer: *stale.CurrentRevisionID == *second.CurrentRevisionID && *stale.PreviousRevisionID == *second.PreviousRevisionID,
		CrossGroupRevisionRejected: crossGroupErr != nil, MissingRevisionRejected: missingRevisionErr != nil,
		ReopenedCurrent: *reopened.CurrentRevisionID, ReopenedPrevious: *reopened.PreviousRevisionID,
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		panic(err)
	}
}
