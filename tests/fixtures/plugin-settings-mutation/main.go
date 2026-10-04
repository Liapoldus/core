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
	"strconv"
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

type fixtureApplier struct {
	calls chan applyCall
}

func (applier fixtureApplier) ApplyConfiguration(_ context.Context, _ string, revision string, configuration []byte) error {
	applier.calls <- applyCall{Revision: revision, Config: string(configuration)}
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
	ctx := context.Background()
	management, err := config.LoadManagement()
	check(err)
	pluginConfigurationWords, err := config.LoadPluginConfiguration()
	check(err)
	auditWords, err := config.LoadAudit()
	check(err)
	errorCatalog, err := config.LoadErrorCatalog()
	check(err)
	sqlite, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqlite.Driver, ParentDirectoryMode: sqlite.ParentDirectoryMode,
		DatabaseFileMode: sqlite.DatabaseFileMode, MaxOpenConnections: sqlite.MaxOpenConnections,
		MaxIdleConnections: sqlite.MaxIdleConnections, SchemaVersion: sqlite.SchemaVersion,
		HasMigrationTableQuery: sqlite.HasMigrationTableQuery, MigrationVersionQuery: sqlite.MigrationVersionQuery,
		SchemaVersionError: sqlite.SchemaVersionError, Pragmas: sqlite.Pragmas,
	}, sqlite.Schema)
	check(err)
	defer database.Close()
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)`,
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
	applier := fixtureApplier{calls: make(chan applyCall, 4)}
	service := &application.PluginConfigurationService{
		Store: configurationStore, Applier: applier,
		Operations:     application.OperationService{Store: operationStore},
		Unavailable:    management.Codes.ManagementUnavailable,
		OperationKind:  management.OperationKinds.PluginSettingsApply,
		PayloadVersion: pluginConfigurationWords.SchemaVersion, MaximumPayloadBytes: pluginConfigurationWords.MaximumPayloadBytes,
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
			Active: pluginConfigurationWords.Slots.Active, Candidate: pluginConfigurationWords.Slots.Staging,
			Failed: "",
		},
		RecoveryAudit: application.PluginConfigurationRecoveryAudit{
			CandidateAction: auditWords.Audit.Actions.PluginSettingsCandidate,
			AppliedAction:   auditWords.Audit.Actions.PluginSettingsApply,
			FailedAction:    auditWords.Audit.Actions.PluginSettingsApplyFailed,
			Succeeded:       auditWords.Audit.Results.Succeeded, Failed: auditWords.Audit.Results.Failed,
		},
	}
	server := &api.Server{
		Token: "fixture-token", Management: management, AuditWords: auditWords, Errors: errorCatalog,
		Operations: application.OperationService{Store: operationStore},
		Audit:      &application.AuditService{Store: auditStore, RetentionDays: auditWords.Audit.RetentionDays},
	}
	handler := api.WithPluginConfigurations(server.Handler(), service)
	settingsPath := management.Paths.Plugins + "/fixture" + management.Paths.PluginSettingsSuffix
	body := []byte("{ \"origin\" : \"new\" }\n")
	accepted := perform(handler, http.MethodPut, settingsPath, body, `"1"`, "same-key-00000001", management)
	var acceptedBody map[string]any
	check(json.Unmarshal(accepted.Body.Bytes(), &acceptedBody))
	operationID, _ := acceptedBody[management.JSON.OperationID].(string)
	if operationID == "" {
		writeReport(map[string]any{"acceptedStatus": accepted.Code, "accepted": acceptedBody})
		return
	}
	operation := waitOperation(handler, operationID, management)
	settings := perform(handler, http.MethodGet, settingsPath, nil, "", "", management)
	replay := perform(handler, http.MethodPut, settingsPath, body, `"1"`, "same-key-00000001", management)
	var replayBody map[string]any
	check(json.Unmarshal(replay.Body.Bytes(), &replayBody))
	replayOperationID, _ := replayBody[management.JSON.OperationID].(string)
	conflictingBody := []byte(`{"origin":"different"}`)
	conflictingReplay := perform(handler, http.MethodPut, settingsPath, conflictingBody, `"1"`, "same-key-00000001", management)
	stale := perform(handler, http.MethodPut, settingsPath, conflictingBody, `"1"`, "stale-key-00000001", management)
	invalid := perform(handler, http.MethodPut, settingsPath, []byte(`{"origin":"one","origin":"two"}`), `"2"`, "invalid-key-00000001", management)
	invalidUTF8 := perform(handler, http.MethodPut, settingsPath, []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, `"2"`, "utf8-key-00000001", management)
	rejectedBody := []byte(`{"reject":true}`)
	tooLargeBody := append([]byte(`{"value":"`), bytes.Repeat([]byte("x"), pluginConfigurationWords.MaximumPayloadBytes)...)
	tooLargeBody = append(tooLargeBody, []byte(`"}`)...)
	tooLarge := perform(handler, http.MethodPut, settingsPath, tooLargeBody, `"2"`, "large-key-00000001", management)
	rejected := perform(handler, http.MethodPut, settingsPath, rejectedBody, `"2"`, "reject-key-00000001", management)
	var rejectedAccepted map[string]any
	check(json.Unmarshal(rejected.Body.Bytes(), &rejectedAccepted))
	rejectedID, _ := rejectedAccepted[management.JSON.OperationID].(string)
	var rejectedOperation map[string]any
	if rejectedID != "" {
		rejectedOperation = waitOperation(handler, rejectedID, management)
		waitForApplyCalls(applier.calls, 2)
	}
	settingsAfterReject := perform(handler, http.MethodGet, settingsPath, nil, "", "", management)
	var settingsBody, rejectedSettingsBody map[string]any
	check(json.Unmarshal(settings.Body.Bytes(), &settingsBody))
	check(json.Unmarshal(settingsAfterReject.Body.Bytes(), &rejectedSettingsBody))
	var staleBody, conflictingBodyMap map[string]any
	check(json.Unmarshal(stale.Body.Bytes(), &staleBody))
	check(json.Unmarshal(conflictingReplay.Body.Bytes(), &conflictingBodyMap))
	var applyCalls []applyCall
	for len(applier.calls) > 0 {
		applyCalls = append(applyCalls, <-applier.calls)
	}
	var activeRaw []byte
	var activeDigest string
	check(database.QueryRowContext(ctx, `SELECT raw_json, sha256 FROM plugin_config_generations WHERE instance_id = ? AND slot = ?`, "fixture", "active").Scan(&activeRaw, &activeDigest))
	rows, err := database.QueryContext(ctx, `SELECT slot FROM plugin_config_generations WHERE instance_id = ? ORDER BY slot`, "fixture")
	check(err)
	var slots []string
	for rows.Next() {
		var slot string
		check(rows.Scan(&slot))
		slots = append(slots, slot)
	}
	check(rows.Err())
	check(rows.Close())
	auditedActor, auditActions := readAudit(ctx, database)
	_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)`,
		"fresh", []byte(`{"name":"fresh"}`), "configured")
	check(err)
	firstRaw := []byte(`{"initial":true}`)
	first := perform(handler, http.MethodPut, management.Paths.Plugins+"/fresh"+management.Paths.PluginSettingsSuffix,
		firstRaw, `"0"`, "first-config-key-0001", management)
	firstReport := map[string]any{"acceptedStatus": first.Code, "state": "", "revision": "", "raw": ""}
	if first.Code == http.StatusAccepted {
		var accepted map[string]any
		check(json.Unmarshal(first.Body.Bytes(), &accepted))
		if id, _ := accepted[management.JSON.OperationID].(string); id != "" {
			finished := waitOperation(handler, id, management)
			firstReport["state"] = finished[management.JSON.State]
		}
		var revision int64
		var raw []byte
		check(database.QueryRowContext(ctx, `SELECT generation, raw_json FROM plugin_config_generations WHERE instance_id = ? AND slot = ?`,
			"fresh", pluginConfigurationWords.Slots.Active).Scan(&revision, &raw))
		firstReport["revision"] = strconv.FormatInt(revision, 10)
		firstReport["raw"] = string(raw)
	}
	writeReport(map[string]any{
		"acceptedStatus": accepted.Code, "accepted": acceptedBody, "operation": operation,
		"applyCalls": applyCalls, "settings": settingsBody,
		"replayStatus": replay.Code, "replayOperationId": replayOperationID,
		"conflictingReplayStatus": conflictingReplay.Code, "conflictingReplayCode": conflictingBodyMap["code"],
		"staleStatus": stale.Code, "staleCode": staleBody["code"], "invalidStatus": invalid.Code, "invalidUTF8Status": invalidUTF8.Code,
		"tooLargeStatus": tooLarge.Code, "activeRaw": string(activeRaw), "activeDigest": activeDigest, "slots": slots,
		"rejectedOperation": rejectedOperation, "settingsAfterReject": rejectedSettingsBody,
		"auditedActor": auditedActor, "auditActions": auditActions,
		"firstConfig": firstReport,
	})
}

func waitOperation(handler http.Handler, id string, management config.ManagementWords) map[string]any {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response := perform(handler, http.MethodGet, management.Paths.Operations+"/"+id, nil, "", "", management)
		var operation map[string]any
		check(json.Unmarshal(response.Body.Bytes(), &operation))
		state, _ := operation[management.JSON.State].(string)
		if state == management.Statuses.Succeeded || state == management.Statuses.Failed {
			return operation
		}
		time.Sleep(10 * time.Millisecond)
	}
	panic("operation did not reach a terminal state")
}

func waitForApplyCalls(calls <-chan applyCall, count int) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(calls) >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	panic("plugin reload was not invoked")
}

func perform(handler http.Handler, method, path string, body []byte, ifMatch, idempotencyKey string, management config.ManagementWords) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytesReader(body))
	request.Header.Set("Authorization", "Bearer fixture-token")
	request.Header.Set(management.Headers.ContentType, management.ContentTypes.JSON)
	if ifMatch != "" {
		request.Header.Set(management.Headers.IfMatch, ifMatch)
	}
	if idempotencyKey != "" {
		request.Header.Set(management.Idempotency.Key, idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func bytesReader(body []byte) *bytes.Reader {
	return bytes.NewReader(body)
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

func writeReport(report map[string]any) {
	check(json.NewEncoder(os.Stdout).Encode(report))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
