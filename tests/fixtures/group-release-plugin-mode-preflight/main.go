package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/caddy"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
)

type report struct {
	MatchingCallAccepted          bool    `json:"matchingCallAccepted"`
	MatchingCallError             string  `json:"matchingCallError"`
	CurrentBeforeMismatch         *string `json:"currentBeforeMismatch"`
	CurrentAfterMismatch          *string `json:"currentAfterMismatch"`
	OperationsBeforeMismatch      int     `json:"operationsBeforeMismatch"`
	OperationsAfterMismatch       int     `json:"operationsAfterMismatch"`
	RevisionsBeforeMismatch       int     `json:"revisionsBeforeMismatch"`
	RevisionsAfterMismatch        int     `json:"revisionsAfterMismatch"`
	MismatchRejectedSynchronously bool    `json:"mismatchRejectedSynchronously"`
}

func main() {
	if len(os.Args) != 5 {
		os.Exit(2)
	}
	ctx := context.Background()
	databasePath, artifactRoot, publicAddress, pluginEndpoint := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	if err := os.MkdirAll(artifactRoot, 0o700); err != nil {
		panic(err)
	}
	sqlite, err := config.LoadSQLiteContract()
	if err != nil {
		panic(err)
	}
	database, err := storage.OpenSQLite(ctx, databasePath, storage.SQLiteOptions{
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

	initialCaddyfile := []byte("http://" + publicAddress + " {\n  respond \"baseline\"\n}\n")
	runtime, _, err := caddy.StartCaddyfileWithPlugins(initialCaddyfile, []caddy.PluginInstance{{
		Name: "fixture", Endpoint: pluginEndpoint, Timeout: 3 * time.Second,
		StartTimeout: 3 * time.Second, MaxConcurrentCalls: 8,
	}})
	if err != nil {
		panic(err)
	}
	defer runtime.Stop()

	groupStore, err := storage.NewSQLiteGroupStore(database)
	if err != nil {
		panic(err)
	}
	artifactStore, err := artifacts.NewGroupReleaseArtifacts(artifactRoot)
	if err != nil {
		panic(err)
	}
	initial, err := artifactStore.StageCaddyfile(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", initialCaddyfile)
	if err != nil {
		panic(err)
	}
	initial.GroupID = "system"
	initial.Actor = "fixture"
	if _, err := groupStore.CreateRevision(ctx, initial); err != nil {
		panic(err)
	}
	if _, err := groupStore.AdvanceCurrent(ctx, "system", initial.ID, nil); err != nil {
		panic(err)
	}
	releaseStore, err := storage.NewSQLiteGroupReleaseStore(database)
	if err != nil {
		panic(err)
	}
	policy, err := config.LoadGroupRelease()
	if err != nil {
		panic(err)
	}
	service := &application.GroupReleaseService{
		Store: groupStore, Releases: releaseStore,
		ContentReader: artifacts.GroupRevisionReader{Root: artifactRoot},
		Artifacts:     artifactStore, Activator: runtime, Policy: policy,
	}

	matching, _, matchingErr := service.Accept(ctx, models.GroupReleaseCommand{
		GroupID: "system", Actor: "fixture", RequestID: "matching-call",
		IdempotencyKey: "matching-call-key", IdempotencyScope: "fixture/matching",
		ExpectedCurrentRevision: stringPointer(initial.ID),
		Caddyfile:               []byte("http://" + publicAddress + " {\n  liapoldus_plugin fixture forms.submit call\n}\n"),
	})
	if matchingErr != nil {
		panic(matchingErr)
	}
	matchingState, err := waitForOperation(ctx, database, matching.ID, policy.SucceededState, policy.FailedState)
	if err != nil || matchingState != policy.SucceededState {
		panic(fmt.Errorf("matching capability mode was not activated"))
	}

	before, err := groupStore.GetPointers(ctx, "system")
	if err != nil {
		panic(err)
	}
	operationsBefore, revisionsBefore := counts(ctx, database)
	mismatch, _, mismatchErr := service.Accept(ctx, models.GroupReleaseCommand{
		GroupID: "system", Actor: "fixture", RequestID: "mismatched-websocket",
		IdempotencyKey: "mismatched-websocket-key", IdempotencyScope: "fixture/mismatch",
		ExpectedCurrentRevision: before.CurrentRevisionID,
		Caddyfile:               []byte("http://" + publicAddress + " {\n  liapoldus_plugin fixture forms.submit websocket\n}\n"),
	})
	after, err := groupStore.GetPointers(ctx, "system")
	if err != nil {
		panic(err)
	}
	if mismatchErr == nil && mismatch.ID != "" {
		if _, err := waitForOperation(ctx, database, mismatch.ID, policy.SucceededState, policy.FailedState); err != nil {
			panic(err)
		}
	}
	operationsAfter, revisionsAfter := counts(ctx, database)
	result := report{
		MatchingCallAccepted: matching.ID != "", MatchingCallError: errorString(matchingErr),
		CurrentBeforeMismatch: before.CurrentRevisionID, CurrentAfterMismatch: after.CurrentRevisionID,
		OperationsBeforeMismatch: operationsBefore, OperationsAfterMismatch: operationsAfter,
		RevisionsBeforeMismatch: revisionsBefore, RevisionsAfterMismatch: revisionsAfter,
		MismatchRejectedSynchronously: mismatchErr != nil && mismatch.ID == "",
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		panic(err)
	}
}

func waitForOperation(ctx context.Context, database *sql.DB, id, succeeded, failed string) (string, error) {
	store, err := storage.NewSQLiteOperationStore(database)
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		operation, err := store.Get(ctx, id)
		if err == nil {
			if operation.State == succeeded {
				return operation.State, nil
			}
			if operation.State == failed {
				return operation.State, nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return "", fmt.Errorf("fixture operation timed out")
}

func counts(ctx context.Context, database *sql.DB) (int, int) {
	var operations, revisions int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM operations").Scan(&operations); err != nil {
		panic(err)
	}
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM group_revisions").Scan(&revisions); err != nil {
		panic(err)
	}
	return operations, revisions
}

func stringPointer(value string) *string { return &value }

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
