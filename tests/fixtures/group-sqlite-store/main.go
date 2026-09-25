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
	FirstCurrent                        string   `json:"firstCurrent"`
	SecondCurrent                       string   `json:"secondCurrent"`
	SecondPrevious                      string   `json:"secondPrevious"`
	StaleCompareAndSwapRejected         bool     `json:"staleCompareAndSwapRejected"`
	StaleCompareAndSwapUnchangedPointer bool     `json:"staleCompareAndSwapUnchangedPointers"`
	CrossGroupRevisionRejected          bool     `json:"crossGroupRevisionRejected"`
	MissingRevisionRejected             bool     `json:"missingRevisionRejected"`
	ReopenedCurrent                     string   `json:"reopenedCurrent"`
	ReopenedPrevious                    string   `json:"reopenedPrevious"`
	GroupOrder                          []string `json:"groupOrder"`
	ArchivedGroupInactive               bool     `json:"archivedGroupInactive"`
	ArchivedGroupHasTimestamp           bool     `json:"archivedGroupHasTimestamp"`
	SystemGroupArchiveRejected          bool     `json:"systemGroupArchiveRejected"`
	FirstRevisionPage                   []string `json:"firstRevisionPage"`
	SecondRevisionPage                  []string `json:"secondRevisionPage"`
	RevisionCursorContinues             bool     `json:"revisionCursorContinues"`
	RevisionCursorEnds                  bool     `json:"revisionCursorEnds"`
	InvalidRevisionLimitRejected        bool     `json:"invalidRevisionLimitRejected"`
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
	store, err := storage.NewSQLiteGroupStore(database)
	if err != nil {
		panic(err)
	}
	if _, err := store.CreateApplicationGroup(ctx, "group-one", models.AuditRecord{Actor: "fixture", Action: "group.create", Resource: "groups", Result: "succeeded", RequestID: "fixture-one"}); err != nil {
		panic(err)
	}
	if _, err := store.CreateApplicationGroup(ctx, "group-two", models.AuditRecord{Actor: "fixture", Action: "group.create", Resource: "groups", Result: "succeeded", RequestID: "fixture-two"}); err != nil {
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
	groups, err := store.ListGroups(ctx)
	if err != nil {
		panic(err)
	}
	groupOrder := make([]string, 0, len(groups.Items))
	for _, group := range groups.Items {
		groupOrder = append(groupOrder, group.ID)
	}
	archivedGroup, err := store.ArchiveGroup(ctx, "group-two")
	if err != nil {
		panic(err)
	}
	_, systemArchiveErr := store.ArchiveGroup(ctx, "system")
	firstPage, err := store.ListRevisions(ctx, "group-one", "", 1)
	if err != nil {
		panic(err)
	}
	secondPage, err := store.ListRevisions(ctx, "group-one", *firstPage.NextCursor, 1)
	if err != nil {
		panic(err)
	}
	_, invalidLimitErr := store.ListRevisions(ctx, "group-one", "", 101)
	firstRevisionIDs := make([]string, 0, len(firstPage.Items))
	secondRevisionIDs := make([]string, 0, len(secondPage.Items))
	for _, revision := range firstPage.Items {
		firstRevisionIDs = append(firstRevisionIDs, revision.ID)
	}
	for _, revision := range secondPage.Items {
		secondRevisionIDs = append(secondRevisionIDs, revision.ID)
	}
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
	store, err = storage.NewSQLiteGroupStore(database)
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
		CrossGroupRevisionRejected:          crossGroupErr != nil, MissingRevisionRejected: missingRevisionErr != nil,
		ReopenedCurrent: *reopened.CurrentRevisionID, ReopenedPrevious: *reopened.PreviousRevisionID,
		GroupOrder: groupOrder, ArchivedGroupInactive: !archivedGroup.Active,
		ArchivedGroupHasTimestamp: archivedGroup.ArchivedAt != nil, SystemGroupArchiveRejected: systemArchiveErr != nil,
		FirstRevisionPage: firstRevisionIDs, SecondRevisionPage: secondRevisionIDs,
		RevisionCursorContinues: firstPage.NextCursor != nil, RevisionCursorEnds: secondPage.NextCursor == nil,
		InvalidRevisionLimitRejected: invalidLimitErr != nil,
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		panic(err)
	}
}
