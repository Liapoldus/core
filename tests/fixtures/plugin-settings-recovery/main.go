package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

type applyCall struct {
	Revision string `json:"revision"`
	Config   string `json:"config"`
}

type operationReport struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	ErrorCode string `json:"errorCode,omitempty"`
}

type fixtureApplier struct {
	calls       []applyCall
	started     chan struct{}
	unavailable bool
}

func (applier *fixtureApplier) ApplyConfiguration(_ context.Context, _ string, revision string, configuration []byte) error {
	applier.calls = append(applier.calls, applyCall{Revision: revision, Config: string(configuration)})
	if applier.started != nil {
		close(applier.started)
		<-make(chan struct{})
	}
	if applier.unavailable {
		return errors.New("injected apply interruption")
	}
	var settings map[string]any
	if err := json.Unmarshal(configuration, &settings); err != nil {
		return err
	}
	if rejected, _ := settings["reject"].(bool); rejected {
		return errors.New("configuration rejected")
	}
	return nil
}

func main() {
	if len(os.Args) < 3 || len(os.Args) > 4 {
		panic("expected phase and database path")
	}
	ctx := context.Background()
	management, err := config.LoadManagement()
	check(err)
	auditWords, err := config.LoadAudit()
	check(err)
	sqlite, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[2], storage.SQLiteOptions{
		Driver: sqlite.Driver, ParentDirectoryMode: sqlite.ParentDirectoryMode,
		DatabaseFileMode: sqlite.DatabaseFileMode, MaxOpenConnections: sqlite.MaxOpenConnections,
		MaxIdleConnections: sqlite.MaxIdleConnections, SchemaVersion: sqlite.SchemaVersion,
		HasMigrationTableQuery: sqlite.HasMigrationTableQuery, MigrationVersionQuery: sqlite.MigrationVersionQuery,
		SchemaVersionError: sqlite.SchemaVersionError, Pragmas: sqlite.Pragmas,
	}, sqlite.Schema)
	check(err)
	defer database.Close()
	if os.Args[1] == "interrupt" {
		interrupt(ctx, database, management, auditWords)
		return
	}
	if os.Args[1] == "reserve-only" {
		reserveOnly(ctx, database, management, auditWords)
		return
	}
	if os.Args[1] == "corrupt-payload" {
		_, err := database.ExecContext(ctx, `UPDATE operation_payloads SET digest = ?`, "invalid-digest")
		check(err)
		write(map[string]any{"corrupted": true})
		return
	}
	if os.Args[1] == "recover" {
		unavailable := len(os.Args) == 4 && os.Args[3] == "unavailable"
		recoverSettings(ctx, database, management, auditWords, unavailable)
		return
	}
	panic("unknown phase")
}

func reserveOnly(ctx context.Context, database *sql.DB, management config.ManagementWords, auditWords config.AuditWords) {
	_, err := database.ExecContext(ctx, `INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)`,
		"fixture", []byte(`{"name":"fixture"}`), "configured")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_config_generations
		(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
		VALUES (?, 1, 'active', ?, ?, 1, '2026-09-29T00:00:00Z')`,
		"fixture", []byte(`{"origin":"old"}`), "d1c2fa5dcee07ed2483d0f5ab8e03cadbd8405d8d7f2fe1c4aa05fb6ca3c0a5b")
	check(err)
	configurationStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	auditStore, err := storage.NewSQLiteAuditStore(database)
	check(err)
	applier := &fixtureApplier{}
	service := newService(configurationStore, operationStore, applier, management, auditWords)
	service.ScheduleWorker = func(func()) {}
	server := newServer(operationStore, auditStore, management, auditWords)
	handler := api.WithPluginConfigurations(server.Handler(), service)
	path := management.Paths.Plugins + "/fixture" + management.Paths.PluginSettingsSuffix
	body := []byte(`{ "origin" : "recovered" }`)
	response := perform(handler, http.MethodPut, path, body, `"1"`, "crash-key-00000001", management)
	var accepted map[string]any
	check(json.Unmarshal(response.Body.Bytes(), &accepted))
	operationID, _ := accepted[management.JSON.OperationID].(string)
	operation, err := operationStore.Get(ctx, operationID)
	check(err)
	payload, found, err := operationStore.Payload(ctx, operationID)
	check(err)
	_, pointers, err := configurationStore.Current(ctx, "fixture")
	check(err)
	write(map[string]any{
		"acceptedStatus": response.Code, "operationId": operationID,
		"candidateRevision": pointers.PendingRevision, "operationState": operation.State, "payloadFound": found,
		"payload": map[string]any{
			"version": payload.Version, "resource": payload.Resource, "expectedRevision": payload.ExpectedRevision,
			"schemaVersion": payload.SchemaVersion, "digest": payload.Digest,
		},
	})
}

func interrupt(ctx context.Context, database *sql.DB, management config.ManagementWords, auditWords config.AuditWords) {
	_, err := database.ExecContext(ctx, `INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)`,
		"fixture", []byte(`{"name":"fixture"}`), "configured")
	check(err)
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_config_generations
		(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
		VALUES (?, 1, 'active', ?, ?, 1, '2026-09-29T00:00:00Z')`,
		"fixture", []byte(`{"origin":"old"}`), "d1c2fa5dcee07ed2483d0f5ab8e03cadbd8405d8d7f2fe1c4aa05fb6ca3c0a5b")
	check(err)
	configurationStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	auditStore, err := storage.NewSQLiteAuditStore(database)
	check(err)
	applier := &fixtureApplier{started: make(chan struct{})}
	service := newService(configurationStore, operationStore, applier, management, auditWords)
	server := newServer(operationStore, auditStore, management, auditWords)
	handler := api.WithPluginConfigurations(server.Handler(), service)
	path := management.Paths.Plugins + "/fixture" + management.Paths.PluginSettingsSuffix
	body := []byte(`{ "origin" : "recovered" }`)
	response := perform(handler, http.MethodPut, path, body, `"1"`, "crash-key-00000001", management)
	var accepted map[string]any
	check(json.Unmarshal(response.Body.Bytes(), &accepted))
	operationID, _ := accepted[management.JSON.OperationID].(string)
	if response.Code != http.StatusAccepted || operationID == "" {
		write(map[string]any{"acceptedStatus": response.Code, "accepted": accepted})
		return
	}
	select {
	case <-applier.started:
	case <-time.After(5 * time.Second):
		panic("candidate did not reach plugin apply before interruption")
	}
	operation, err := operationStore.Get(ctx, operationID)
	check(err)
	active, pointers, err := configurationStore.Current(ctx, "fixture")
	check(err)
	write(map[string]any{
		"acceptedStatus": response.Code, "operationId": operation.ID,
		"candidateRevision": active.Revision, "activeRevision": active.Revision, "pendingRevision": pointers.PendingRevision,
		"operationState": operation.State,
	})
}

func recoverSettings(ctx context.Context, database *sql.DB, management config.ManagementWords, auditWords config.AuditWords, unavailable bool) {
	configurationStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	auditStore, err := storage.NewSQLiteAuditStore(database)
	check(err)
	applier := &fixtureApplier{calls: make([]applyCall, 0), unavailable: unavailable}
	service := newService(configurationStore, operationStore, applier, management, auditWords)
	recoveryErr := service.Recover(ctx)
	managementReady := recoveryErr == nil
	if unavailable && managementReady {
		operation, operationErr := operationStore.Get(ctx, latestOperationID(ctx, database))
		check(operationErr)
		settings, _, currentErr := configurationStore.Current(ctx, "fixture")
		check(currentErr)
		_, pointers, currentErr := configurationStore.Current(ctx, "fixture")
		check(currentErr)
		write(map[string]any{
			"managementReady": true, "applyCalls": applier.calls,
			"operation":       operationReport{ID: operation.ID, State: operation.State, ErrorCode: operation.ErrorCode},
			"settings":        map[string]any{"revision": settings.Revision, "config": json.RawMessage(settings.SettingsJSON)},
			"pendingRevision": pointers.PendingRevision, "instanceFenced": service.IsInstanceFenced("fixture"),
		})
		return
	}
	server := newServer(operationStore, auditStore, management, auditWords)
	if managementReady {
		handler := api.WithPluginConfigurations(server.Handler(), service)
		path := management.Paths.Plugins + "/fixture" + management.Paths.PluginSettingsSuffix
		body := []byte(`{ "origin" : "recovered" }`)
		replay := perform(handler, http.MethodPut, path, body, `"1"`, "crash-key-00000001", management)
		var replayBody map[string]any
		check(json.Unmarshal(replay.Body.Bytes(), &replayBody))
		replayOperationID, ok := replayBody[management.JSON.OperationID].(string)
		if !ok {
			write(map[string]any{"replayStatus": replay.Code, "replayBody": replayBody})
			return
		}
		operation, operationErr := operationStore.Get(ctx, replayOperationID)
		check(operationErr)
		settings, _, currentErr := configurationStore.Current(ctx, "fixture")
		check(currentErr)
		_, pointers, currentErr := configurationStore.Current(ctx, "fixture")
		check(currentErr)
		auditActor, actions := readAudit(ctx, database)
		write(map[string]any{
			"recoverySupported": true, "managementReady": managementReady,
			"applyCalls": applier.calls, "operation": operationReport{ID: operation.ID, State: operation.State, ErrorCode: operation.ErrorCode},
			"settings":        map[string]any{"revision": settings.Revision, "config": json.RawMessage(settings.SettingsJSON)},
			"pendingRevision": pointers.PendingRevision,
			"replayStatus":    replay.Code, "replayOperationId": replayBody[management.JSON.OperationID],
			"auditActor": auditActor, "auditActions": actions,
		})
		return
	}
	operation, operationErr := operationStore.Get(ctx, latestOperationID(ctx, database))
	check(operationErr)
	settings, _, currentErr := configurationStore.Current(ctx, "fixture")
	check(currentErr)
	_, pointers, currentErr := configurationStore.Current(ctx, "fixture")
	check(currentErr)
	_, actions := readAudit(ctx, database)
	write(map[string]any{
		"recoverySupported": true, "managementReady": managementReady,
		"applyCalls": applier.calls, "operation": operationReport{ID: operation.ID, State: operation.State, ErrorCode: operation.ErrorCode},
		"settings":        map[string]any{"revision": settings.Revision, "config": json.RawMessage(settings.SettingsJSON)},
		"pendingRevision": pointers.PendingRevision,
		"auditActions":    actions,
	})
}

func newService(configurationStore *storage.SQLitePluginConfigurationStore, operationStore *storage.SQLiteOperationStore, applier *fixtureApplier, management config.ManagementWords, auditWords config.AuditWords) *application.PluginConfigurationService {
	pluginConfigWords, err := config.LoadPluginConfiguration()
	check(err)
	return &application.PluginConfigurationService{
		Store: configurationStore, Applier: applier, Operations: application.OperationService{Store: operationStore},
		Unavailable:    management.Codes.ManagementUnavailable,
		OperationKind:  management.OperationKinds.PluginSettingsApply,
		PayloadVersion: pluginConfigWords.SchemaVersion, MaximumPayloadBytes: pluginConfigWords.MaximumPayloadBytes,
		PayloadFailureCode: management.Codes.ActivationFailed,
		OperationStates: application.PluginConfigurationOperationStates{
			Pending: management.Statuses.Pending, Running: management.Statuses.Running,
			Succeeded: management.Statuses.Succeeded, Failed: management.Statuses.Failed,
		},
		OperationFailureCodes: application.PluginConfigurationFailureCodes{
			Rejected: management.Codes.PluginConfigInvalid, Conflict: management.Codes.PluginRevisionConflict,
			Unavailable: management.Codes.PluginUnavailable, ApplyFailed: management.Codes.ActivationFailed,
		},
		RevisionStates: application.PluginConfigurationRevisionStates{
			Active: pluginConfigWords.Slots.Active, Candidate: pluginConfigWords.Slots.Staging,
			Failed: "",
		},
		RecoveryAudit: application.PluginConfigurationRecoveryAudit{
			CandidateAction: auditWords.Audit.Actions.PluginSettingsCandidate,
			AppliedAction:   auditWords.Audit.Actions.PluginSettingsApply,
			FailedAction:    auditWords.Audit.Actions.PluginSettingsApplyFailed,
			Succeeded:       auditWords.Audit.Results.Succeeded, Failed: auditWords.Audit.Results.Failed,
		},
	}
}

func newServer(operationStore *storage.SQLiteOperationStore, auditStore *storage.SQLiteAuditStore, management config.ManagementWords, auditWords config.AuditWords) *api.Server {
	return &api.Server{
		Token: "fixture-token", Management: management, AuditWords: auditWords,
		Operations: application.OperationService{Store: operationStore},
		Audit:      &application.AuditService{Store: auditStore, RetentionDays: auditWords.Audit.RetentionDays},
	}
}

func perform(handler http.Handler, method, path string, body []byte, ifMatch, idempotencyKey string, management config.ManagementWords) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture-token")
	request.Header.Set(management.Headers.ContentType, management.ContentTypes.JSON)
	request.Header.Set(management.Headers.IfMatch, ifMatch)
	request.Header.Set(management.Idempotency.Key, idempotencyKey)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func latestOperationID(ctx context.Context, database *sql.DB) string {
	var id string
	check(database.QueryRowContext(ctx, `SELECT id FROM operations ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&id))
	return id
}

func readAudit(ctx context.Context, database *sql.DB) (string, []string) {
	rows, err := database.QueryContext(ctx, `SELECT actor, action FROM audit_events ORDER BY sequence`)
	check(err)
	defer rows.Close()
	actor := ""
	actions := make([]string, 0)
	for rows.Next() {
		var currentActor, action string
		check(rows.Scan(&currentActor, &action))
		if actor == "" {
			actor = currentActor
		}
		actions = append(actions, action)
	}
	check(rows.Err())
	return actor, actions
}

func write(value any) {
	check(json.NewEncoder(os.Stdout).Encode(value))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
