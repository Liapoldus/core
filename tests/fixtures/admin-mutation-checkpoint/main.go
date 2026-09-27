package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/caddy"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

const sensitivePayload = "ADMIN_BODY_MUST_NOT_BE_AUDITED"

func main() {
	if len(os.Args) != 2 {
		panic("expected database path")
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

	var adminRequests atomic.Int64
	var checkpointCountAtForward atomic.Int64
	var auditCountAtForward atomic.Int64
	admin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		adminRequests.Add(1)
		if request.Method == http.MethodGet && request.URL.Path == "/config/" {
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"apps":{"http":{"servers":{}}}}`)
			return
		}
		checkpointCountAtForward.Store(count(database, `SELECT COUNT(*) FROM caddy_checkpoints`))
		auditCountAtForward.Store(count(database, `SELECT COUNT(*) FROM audit_events`))
		_, _ = io.Copy(io.Discard, request.Body)
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
	server := (&api.Server{
		Token: "fixture-management-token", Management: management,
		Errors: errorCatalog, AuditWords: auditWords,
		AdminWords: adminWords, AdminMutations: adminMutationService,
	}).Handler()
	request := httptest.NewRequest(http.MethodPut, "/api/caddy/config/apps/http/servers/srv0", strings.NewReader(`{"value":"`+sensitivePayload+`"}`))
	request.Header.Set("Authorization", "Bearer fixture-management-token")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

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
