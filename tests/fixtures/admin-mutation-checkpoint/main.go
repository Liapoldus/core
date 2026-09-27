package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/caddy"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

const sensitivePayload = "ADMIN_BODY_MUST_NOT_BE_AUDITED"

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 || (len(os.Args) == 3 && os.Args[2] != "drift") {
		panic("expected database path")
	}
	driftMode := len(os.Args) == 3
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

	var adminRequests atomic.Int64
	var checkpointCountAtForward atomic.Int64
	var auditCountAtForward atomic.Int64
	liveSnapshot := []byte(`{"apps":{"http":{"servers":{}}}}`)
	admin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		adminRequests.Add(1)
		if request.Method == http.MethodGet && request.URL.Path == "/config/" {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write(liveSnapshot)
			return
		}
		checkpointCountAtForward.Store(count(database, `SELECT COUNT(*) FROM caddy_checkpoints`))
		auditCountAtForward.Store(count(database, `SELECT COUNT(*) FROM audit_events`))
		_, _ = io.Copy(io.Discard, request.Body)
		if driftMode {
			liveSnapshot = []byte(`{"apps":{"http":{"servers":{"manual-change":{}}}}}`)
		}
		http.Error(response, "admin mutation rejected", http.StatusServiceUnavailable)
	}))
	defer admin.Close()

	management, err := config.LoadManagement()
	if err != nil {
		panic(err)
	}
	errorCatalog, err := config.LoadErrorCatalog()
	if err != nil {
		panic(err)
	}
	adminWords, err := config.LoadAdminMutation()
	if err != nil {
		panic(err)
	}
	adminTimeout, err := time.ParseDuration(adminWords.Timeouts.Request)
	if err != nil {
		panic(err)
	}
	adminClient, err := caddy.NewAdminClient(admin.URL, admin.Client(), caddy.AdminClientOptions{
		SnapshotPath: adminWords.Paths.Snapshot, PathPrefix: adminWords.Paths.LeadingSlash,
		RequestBodyBytes: adminWords.Limits.RequestBodyBytes, SnapshotBytes: adminWords.Limits.SnapshotBytes,
		ResponseBodyBytes: adminWords.Limits.ResponseBodyBytes, RequestTimeout: adminTimeout,
		ForwardRequestHeaders: adminWords.Headers.ForwardRequest, ForwardResponseHeaders: adminWords.Headers.ForwardResponse,
		InvalidConfiguration: adminWords.Diagnostics.InvalidConfiguration,
		SnapshotUnavailable:  adminWords.Diagnostics.SnapshotUnavailable, AdminUnavailable: adminWords.Diagnostics.AdminUnavailable,
	})
	if err != nil {
		panic(err)
	}
	checkpointStore, err := storage.NewSQLiteCaddyCheckpointStore(database)
	if err != nil {
		panic(err)
	}
	checkpointArtifacts, err := artifacts.NewCaddyCheckpointArtifacts(filepath.Dir(os.Args[1]), artifacts.CaddyCheckpointOptions{
		Directory: adminWords.Paths.CheckpointDirectory, CheckpointSuffix: adminWords.Paths.CheckpointSuffix,
		TemporarySuffix: adminWords.Paths.TemporarySuffix, DirectoryMode: adminWords.Modes.Directory,
		FileMode: adminWords.Modes.File, InvalidConfiguration: adminWords.Diagnostics.InvalidConfiguration,
	})
	if err != nil {
		panic(err)
	}
	adminMutationService := &application.AdminMutationService{
		Admin: adminClient, Checkpoints: checkpointStore, Artifacts: checkpointArtifacts,
		Policy: application.AdminMutationPolicy{
			MutationMethods:       adminWords.Methods.Mutating,
			SuccessStatusMinimum:  adminWords.Statuses.SuccessMinimum,
			SuccessStatusMaximum:  adminWords.Statuses.SuccessMaximum,
			MaximumSnapshotBytes:  adminWords.Limits.SnapshotBytes,
			OperationKind:         adminWords.Operation.Kind,
			OperationRunning:      adminWords.Operation.Running,
			OperationSucceeded:    adminWords.Operation.Succeeded,
			OperationFailed:       adminWords.Operation.Failed,
			AuditAction:           adminWords.Audit.Action,
			AuditResource:         adminWords.Audit.Resource,
			AuditStarted:          adminWords.Audit.Started,
			AuditSucceeded:        adminWords.Audit.Succeeded,
			AuditFailed:           adminWords.Audit.Failed,
			InvalidConfiguration:  adminWords.Diagnostics.InvalidConfiguration,
			SnapshotUnavailable:   adminWords.Diagnostics.SnapshotUnavailable,
			CheckpointUnavailable: adminWords.Diagnostics.CheckpointUnavailable,
			AdminUnavailable:      adminWords.Diagnostics.AdminUnavailable,
		},
	}
	auditWords, err := config.LoadAudit()
	if err != nil {
		panic(err)
	}
	server := &api.Server{
		Token: "fixture-management-token", Management: management,
		Errors: errorCatalog, AuditWords: auditWords,
		AdminWords: adminWords, AdminMutations: adminMutationService,
	}
	var groupStore *storage.SQLiteGroupStore
	var releasePolicy models.GroupReleasePolicy
	var initialRevisionID string
	var initialRevisionCount int64
	if driftMode {
		groupStore, err = storage.NewSQLiteGroupStore(database)
		if err != nil {
			panic(err)
		}
		if _, err := groupStore.CreateApplicationGroup(ctx, "application-drift", models.AuditRecord{
			Actor: "fixture", Action: "group.create", Resource: "groups", Result: "succeeded", RequestID: "fixture-group-create",
		}); err != nil {
			panic(err)
		}
		initialRevisionID = strings.Repeat("a", 64)
		initialCaddyfile := []byte("example.test {\n  respond 200\n}\n")
		if _, err := groupStore.CreateRevision(ctx, models.GroupRevision{
			ID: initialRevisionID, GroupID: "application-drift", CaddyfileDigest: strings.Repeat("b", 64),
			CaddyfilePath: "initial.caddyfile", Actor: "fixture",
		}); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(os.Args[1]), "initial.caddyfile"), initialCaddyfile, 0o600); err != nil {
			panic(err)
		}
		if _, err := groupStore.AdvanceCurrent(ctx, "application-drift", initialRevisionID, nil); err != nil {
			panic(err)
		}
		initialRevisionCount = count(database, `SELECT COUNT(*) FROM group_revisions`)
		releaseStore, storeErr := storage.NewSQLiteGroupReleaseStore(database)
		if storeErr != nil {
			panic(storeErr)
		}
		releasePolicy, err = config.LoadGroupRelease()
		if err != nil {
			panic(err)
		}
		releaseArtifacts, artifactErr := artifacts.NewGroupReleaseArtifacts(filepath.Dir(os.Args[1]))
		if artifactErr != nil {
			panic(artifactErr)
		}
		server.GroupReleases = &application.GroupReleaseService{
			Store: groupStore, Releases: releaseStore,
			ContentReader: artifacts.GroupRevisionReader{Root: filepath.Dir(os.Args[1])},
			Artifacts:     releaseArtifacts, Activator: &fixtureDriftActivator{}, DriftGuard: adminMutationService, Policy: releasePolicy,
		}
		server.GroupReleasePolicy = releasePolicy
		server.GroupService = application.GroupService{Store: groupStore, ContentReader: artifacts.GroupRevisionReader{Root: filepath.Dir(os.Args[1])}}
	}
	handler := server.Handler()
	request := httptest.NewRequest(http.MethodPut, "/api/caddy/config/apps/http/servers/srv0", strings.NewReader(`{"value":"`+sensitivePayload+`"}`))
	request.Header.Set("Authorization", "Bearer fixture-management-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var publishStatus int
	var publishCode string
	var drift bool
	var currentRevisionUnchanged bool
	var revisionCountUnchanged bool
	if driftMode {
		initialDigest := sha256.Sum256([]byte(`{"apps":{"http":{"servers":{}}}}`))
		observedDigest := sha256.Sum256(liveSnapshot)
		drift = observedDigest != initialDigest
		publish := performDriftPublish(handler, initialRevisionID, releasePolicy)
		publishStatus = publish.Code
		var problem map[string]any
		if err := json.Unmarshal(publish.Body.Bytes(), &problem); err != nil {
			panic(err)
		}
		publishCode, _ = problem["code"].(string)
		if publishStatus == http.StatusAccepted {
			operationID, _ := problem[management.JSON.OperationID].(string)
			waitForGroupOperation(database, operationID, releasePolicy)
		}
		pointers, err := groupStore.GetPointers(ctx, "application-drift")
		if err != nil {
			panic(err)
		}
		currentRevisionUnchanged = pointers.CurrentRevisionID != nil && *pointers.CurrentRevisionID == initialRevisionID
		revisionCountUnchanged = count(database, `SELECT COUNT(*) FROM group_revisions`) == initialRevisionCount
	}

	var auditBodyStored bool
	rows, err := database.Query(`SELECT actor, action, resource, result, request_id, COALESCE(before_digest, ''), COALESCE(after_digest, '') FROM audit_events`)
	if err != nil {
		panic(err)
	}
	for rows.Next() {
		var fields [7]string
		if err := rows.Scan(&fields[0], &fields[1], &fields[2], &fields[3], &fields[4], &fields[5], &fields[6]); err != nil {
			panic(err)
		}
		for _, field := range fields {
			auditBodyStored = auditBodyStored || strings.Contains(field, sensitivePayload)
		}
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
	if err := rows.Close(); err != nil {
		panic(err)
	}

	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"status": response.Code, "adminRequests": adminRequests.Load(),
		"checkpointCountAtForward": checkpointCountAtForward.Load(),
		"auditCountAtForward":      auditCountAtForward.Load(),
		"checkpointCountAfter":     count(database, `SELECT COUNT(*) FROM caddy_checkpoints`),
		"auditBodyStored":          auditBodyStored,
		"mutationStatus":           response.Code, "publishStatus": publishStatus, "publishCode": publishCode,
		"drift": drift, "currentRevisionUnchanged": currentRevisionUnchanged,
		"revisionCountUnchanged": revisionCountUnchanged,
	}); err != nil {
		panic(err)
	}
}

func count(database *sql.DB, query string) int64 {
	var value int64
	if err := database.QueryRow(query).Scan(&value); err != nil {
		panic(err)
	}
	return value
}

func performDriftPublish(handler http.Handler, expectedCurrentRevision string, policy models.GroupReleasePolicy) *httptest.ResponseRecorder {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadata, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {"form-data; name=\"metadata\""},
		"Content-Type":        {policy.MetadataContentType},
	})
	if err != nil {
		panic(err)
	}
	if err := json.NewEncoder(metadata).Encode(map[string]string{
		policy.MetadataIdempotencyKeyField:   "admin-drift-publish-0001",
		policy.MetadataExpectedRevisionField: expectedCurrentRevision,
	}); err != nil {
		panic(err)
	}
	caddyfile, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {"form-data; name=\"caddyfile\"; filename=\"Caddyfile\""},
		"Content-Type":        {policy.CaddyfileContentType},
	})
	if err != nil {
		panic(err)
	}
	if _, err := io.WriteString(caddyfile, "example.test {\n  respond 201\n}\n"); err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/groups/application-drift/releases", &body)
	request.Header.Set("Authorization", "Bearer fixture-management-token")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func waitForGroupOperation(database *sql.DB, id string, policy models.GroupReleasePolicy) {
	store, err := storage.NewSQLiteOperationStore(database)
	if err != nil {
		panic(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		operation, err := store.Get(context.Background(), id)
		if err != nil {
			panic(err)
		}
		if operation.State == policy.SucceededState || operation.State == policy.FailedState {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	panic("group release operation did not finish")
}

type fixtureDriftActivator struct{}

func (*fixtureDriftActivator) Validate(_ context.Context, source []byte) error {
	_, _, err := caddy.AdaptCaddyfile(source)
	return err
}

func (*fixtureDriftActivator) Activate(context.Context, []byte) error { return nil }
