package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type activator struct {
	active string
}

func (*activator) Validate(context.Context, []byte) error { return nil }

func (state *activator) Activate(_ context.Context, snapshot []byte) error {
	state.active = string(snapshot)
	return nil
}

func main() {
	ctx := context.Background()
	databasePath := os.Args[1]
	root := filepath.Dir(databasePath)
	sqliteContract, err := config.LoadSQLiteContract()
	check(err)
	database := openDatabase(ctx, databasePath, sqliteContract)
	groupStore, err := storage.NewSQLiteGroupStore(database)
	check(err)
	group, err := groupStore.CreateApplicationGroup(ctx, "recovery-group", models.AuditRecord{
		Actor: "fixture", Action: "group.create", Resource: "recovery-group", Result: "succeeded", RequestID: "request-create",
	})
	check(err)

	previous := createRevision(ctx, groupStore, root, "previous", "previous-release")
	_, err = groupStore.AdvanceCurrent(ctx, group.ID, previous.ID, nil)
	check(err)
	current := createRevision(ctx, groupStore, root, "current", "current-release")
	_, err = groupStore.AdvanceCurrent(ctx, group.ID, current.ID, &previous.ID)
	check(err)
	pointersBefore, err := groupStore.GetPointers(ctx, group.ID)
	check(err)

	policy, err := config.LoadGroupRelease()
	check(err)
	releaseStore, err := storage.NewSQLiteGroupReleaseStore(database)
	check(err)
	artifactStore, err := artifacts.NewGroupReleaseArtifacts(root)
	check(err)
	candidate, err := artifactStore.StageCaddyfile(ctx, strings.Repeat("c", 64), []byte("candidate-release"))
	check(err)
	candidate.GroupID = group.ID
	artifact, err := artifactStore.StageArtifact(ctx, candidate.ID, archive("frontends/ui/index.html"), policy)
	check(err)
	candidate.ArtifactDigest = artifact.ArtifactDigest
	candidate.ArtifactPath = artifact.ArtifactPath
	now := time.Now().UTC()
	reservation := models.GroupReleaseReservation{
		GroupID: group.ID, ExpectedCurrentRevision: &current.ID, Actor: "fixture",
		Scope: "groups/recovery-group/releases", KeyDigest: strings.Repeat("d", 64),
		RequestDigest: strings.Repeat("e", 64), OperationID: strings.Repeat("f", 64), RevisionID: candidate.ID,
		OperationKind: policy.OperationKind, OperationState: policy.PendingState, RequestID: "request-recovery",
		CaddyfileDigest: candidate.CaddyfileDigest, ArtifactDigest: candidate.ArtifactDigest,
		CaddyfilePath: candidate.CaddyfilePath, ArtifactPath: candidate.ArtifactPath,
		CreatedAt: now, ExpiresAt: now.Add(policy.IdempotencyWindow),
	}
	_, _, err = releaseStore.Reserve(ctx, reservation)
	check(err)

	// Model a crash after the candidate reached the runtime and before SQLite commit.
	runtime := &activator{}
	check(runtime.Activate(ctx, []byte("candidate-release")))
	activeBeforeRestart := runtime.active
	check(database.Close())
	database = openDatabase(ctx, databasePath, sqliteContract)
	defer database.Close()
	groupStore, err = storage.NewSQLiteGroupStore(database)
	check(err)
	releaseStore, err = storage.NewSQLiteGroupReleaseStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	service := &application.GroupReleaseService{
		Store: groupStore, Releases: releaseStore,
		ContentReader: artifacts.GroupRevisionReader{Root: root},
		Artifacts:     artifactStore, Activator: runtime, Policy: policy,
	}
	check(service.ActivateCurrent(ctx))
	activeAfterCurrent := runtime.active
	check(service.Recover(ctx))
	activeAfterRecovery := runtime.active

	pointersAfter, err := groupStore.GetPointers(ctx, group.ID)
	check(err)
	operation, err := operationStore.Get(ctx, reservation.OperationID)
	check(err)
	var journalState string
	check(database.QueryRowContext(ctx, "SELECT state FROM group_release_journal WHERE operation_id = ?", reservation.OperationID).Scan(&journalState))
	pending, err := releaseStore.Pending(ctx)
	check(err)
	_, caddyfileStatErr := os.Stat(filepath.Join(root, filepath.FromSlash(candidate.CaddyfilePath)))
	_, artifactStatErr := os.Stat(filepath.Join(root, filepath.FromSlash(*candidate.ArtifactPath)))
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{
		"runtimeBeforeRestart":          activeBeforeRestart,
		"runtimeAfterCurrentActivation": activeAfterCurrent,
		"runtimeAfterRecovery":          activeAfterRecovery,
		"currentBeforeRecovery":         *pointersBefore.CurrentRevisionID,
		"previousBeforeRecovery":        *pointersBefore.PreviousRevisionID,
		"currentAfterRecovery":          *pointersAfter.CurrentRevisionID,
		"previousAfterRecovery":         *pointersAfter.PreviousRevisionID,
		"operationState":                operation.State,
		"journalState":                  journalState,
		"pendingCount":                  len(pending),
		"stagedCaddyfileExists":         caddyfileStatErr == nil,
		"stagedArtifactExists":          artifactStatErr == nil,
	}))
}

func createRevision(ctx context.Context, store *storage.SQLiteGroupStore, root, id, content string) models.GroupRevision {
	path := id + ".caddyfile"
	check(os.WriteFile(filepath.Join(root, path), []byte(content), 0o600))
	revision, err := store.CreateRevision(ctx, models.GroupRevision{
		ID: strings.Repeat(id[:1], 64), GroupID: "recovery-group",
		CaddyfileDigest: digest(content), CaddyfilePath: path,
	})
	check(err)
	return revision
}

func digest(value string) string {
	contents := []byte(value)
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

func archive(path string) []byte {
	var compressed bytes.Buffer
	compressor := gzip.NewWriter(&compressed)
	writer := tar.NewWriter(compressor)
	contents := []byte("fixture")
	check(writer.WriteHeader(&tar.Header{Name: path, Mode: 0o600, Size: int64(len(contents)), Typeflag: tar.TypeReg}))
	_, err := writer.Write(contents)
	check(err)
	check(writer.Close())
	check(compressor.Close())
	return compressed.Bytes()
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
		panic(err)
	}
}
