package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	ctx := context.Background()
	sqlite, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqlite.Driver, ParentDirectoryMode: sqlite.ParentDirectoryMode,
		DatabaseFileMode: sqlite.DatabaseFileMode, MaxOpenConnections: sqlite.MaxOpenConnections,
		MaxIdleConnections: sqlite.MaxIdleConnections, SchemaVersion: sqlite.SchemaVersion,
		HasMigrationTableQuery: sqlite.HasMigrationTableQuery, MigrationVersionQuery: sqlite.MigrationVersionQuery,
		SchemaVersionError: sqlite.SchemaVersionError, Pragmas: sqlite.Pragmas,
	}, sqlite.Schema)
	if err != nil {
		panic(err)
	}
	defer database.Close()

	groupStore, err := storage.NewSQLiteGroupStore(database)
	if err != nil {
		panic(err)
	}
	pointers, err := groupStore.GetPointers(ctx, sqlite.SystemGroupID)
	if err != nil || pointers.CurrentRevisionID == nil {
		panic(fmt.Errorf("active system revision is required"))
	}
	policy, err := config.LoadGroupRelease()
	if err != nil {
		panic(err)
	}
	artifactStore, err := artifacts.NewGroupReleaseArtifacts(os.Args[2])
	if err != nil {
		panic(err)
	}
	const revisionID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	revision, err := artifactStore.StageCaddyfile(ctx, revisionID, []byte("http://127.0.0.1:0 { respond pending }\n"))
	if err != nil {
		panic(err)
	}
	revision.GroupID = sqlite.SystemGroupID
	revision.Actor = "recovery-fixture"
	releaseStore, err := storage.NewSQLiteGroupReleaseStore(database)
	if err != nil {
		panic(err)
	}
	now := time.Now().UTC()
	operationID := strings.Repeat("c", 64)
	_, duplicate, err := releaseStore.Reserve(ctx, models.GroupReleaseReservation{
		GroupID: sqlite.SystemGroupID, ExpectedCurrentRevision: pointers.CurrentRevisionID,
		Actor: revision.Actor, Scope: "fixture/recovery", KeyDigest: strings.Repeat("d", 64),
		RequestDigest: strings.Repeat("e", 64), OperationID: operationID, RevisionID: revision.ID,
		OperationKind: policy.OperationKind, OperationState: policy.PendingState,
		RequestID: operationID, CaddyfileDigest: revision.CaddyfileDigest,
		CaddyfilePath: revision.CaddyfilePath, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil || duplicate {
		panic(fmt.Errorf("pending release reservation could not be created"))
	}
	trigger := fmt.Sprintf("CREATE TRIGGER reject_recovery_transition BEFORE UPDATE ON operations WHEN OLD.id = '%s' BEGIN SELECT RAISE(FAIL, 'fixture'); END", operationID)
	if _, err := database.ExecContext(ctx, trigger); err != nil {
		panic(err)
	}
}
