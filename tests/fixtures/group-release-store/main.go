package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func main() {
	ctx := context.Background()
	contract, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	check(err)
	groupStore, err := storage.NewSQLiteGroupStore(database)
	check(err)
	group, err := groupStore.CreateApplicationGroup(ctx, "release-group", models.AuditRecord{Actor: "fixture", Action: "group.create", Resource: "release-group", Result: "succeeded", RequestID: "request-create"})
	check(err)
	initialRevision := models.GroupRevision{ID: "revision-initial", GroupID: group.ID, CaddyfileDigest: "initial-digest", CaddyfilePath: "initial.caddyfile"}
	_, err = groupStore.CreateRevision(ctx, initialRevision)
	check(err)
	_, err = groupStore.AdvanceCurrent(ctx, group.ID, initialRevision.ID, nil)
	check(err)
	releaseStore, err := storage.NewSQLiteGroupReleaseStore(database)
	check(err)
	now := time.Now().UTC()
	reservation := models.GroupReleaseReservation{
		GroupID: group.ID, ExpectedCurrentRevision: &initialRevision.ID, Actor: "fixture", Scope: "groups/release-group/releases",
		KeyDigest: "key-digest-one", RequestDigest: "request-digest-one", OperationID: "operation-one", RevisionID: "revision-next",
		OperationKind: "group.publish", OperationState: "pending", RequestID: "request-publish",
		CaddyfileDigest: "caddyfile-digest", CaddyfilePath: "releases/revision-next/Caddyfile",
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	accepted, duplicate, err := releaseStore.Reserve(ctx, reservation)
	check(err)
	retried, retryDuplicate, err := releaseStore.Reserve(ctx, reservation)
	check(err)
	conflicting := reservation
	conflicting.RequestDigest = "different-request-digest"
	_, _, conflictErr := releaseStore.Reserve(ctx, conflicting)
	var idempotencyConflict models.IdempotencyConflict
	conflictDetected := errors.As(conflictErr, &idempotencyConflict)
	check(releaseStore.Commit(ctx, models.GroupReleaseCommit{
		GroupID: group.ID, ExpectedCurrentRevision: &initialRevision.ID,
		Revision:    models.GroupRevision{ID: reservation.RevisionID, GroupID: group.ID, CaddyfileDigest: reservation.CaddyfileDigest, CaddyfilePath: reservation.CaddyfilePath, Actor: reservation.Actor},
		OperationID: reservation.OperationID, OperationState: "succeeded", JournalState: "succeeded", UpdatedAt: now,
		Audit: models.AuditRecord{Timestamp: now, Actor: "fixture", Action: "group.publish", Resource: group.ID, Result: "succeeded", RequestID: reservation.RequestID},
	}))
	pointers, err := groupStore.GetPointers(ctx, group.ID)
	check(err)
	check(database.Close())
	database, err = storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: contract.Driver, ParentDirectoryMode: contract.ParentDirectoryMode,
		DatabaseFileMode: contract.DatabaseFileMode, MaxOpenConnections: contract.MaxOpenConnections,
		MaxIdleConnections: contract.MaxIdleConnections, SchemaVersion: contract.SchemaVersion,
		HasMigrationTableQuery: contract.HasMigrationTableQuery, MigrationVersionQuery: contract.MigrationVersionQuery,
		SchemaVersionError: contract.SchemaVersionError, Pragmas: contract.Pragmas,
	}, contract.Schema)
	check(err)
	defer database.Close()
	groupStore, err = storage.NewSQLiteGroupStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	reopenedOperation, err := operationStore.Get(ctx, reservation.OperationID)
	check(err)
	reopenedPointers, err := groupStore.GetPointers(ctx, group.ID)
	check(err)
	releaseStore, err = storage.NewSQLiteGroupReleaseStore(database)
	check(err)
	pending, err := releaseStore.Pending(ctx)
	check(err)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"acceptedOperation": accepted.ID, "firstWasDuplicate": duplicate,
		"retriedOperation": retried.ID, "retryWasDuplicate": retryDuplicate,
		"idempotencyConflict": conflictDetected, "committedCurrent": pointers.CurrentRevisionID,
		"reopenedCurrent": reopenedPointers.CurrentRevisionID, "reopenedOperationState": reopenedOperation.State,
		"pendingCountAfterCommit": len(pending),
	}))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
