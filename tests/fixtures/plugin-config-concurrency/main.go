package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/api"
)

const (
	instanceCount     = 8
	roundsPerInstance = 6
	settleTimeout     = 60 * time.Second
)

// slowApplier widens the window in which apply workers overlap, and counts every
// reload it is asked to perform. A correct implementation invokes it exactly
// once per operation, so appliedOperations is also an at-most-once assertion.
type slowApplier struct {
	calls atomic.Int64
}

func (applier *slowApplier) ApplyConfiguration(_ context.Context, _ string, _ string, _ []byte) error {
	applier.calls.Add(1)
	time.Sleep(2 * time.Millisecond)
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

	instanceIDs := make([]string, 0, instanceCount)
	for index := 0; index < instanceCount; index++ {
		instanceID := fmt.Sprintf("race-%d", index)
		instanceIDs = append(instanceIDs, instanceID)
		_, err = database.ExecContext(ctx, `INSERT INTO plugin_instances (id, mode, manifest_json, state) VALUES (?, ?, ?, ?)`,
			instanceID, "remote", []byte(`{"name":"race"}`), "configured")
		check(err)
		_, err = database.ExecContext(ctx, `INSERT INTO plugin_config_generations
			(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
			VALUES (?, 1, 'active', ?, ?, 1, '2026-09-29T00:00:00Z')`,
			instanceID, []byte(`{"origin":"initial"}`),
			"0ac77dfda2ddf5bf8041bbca0277b74fdceef5112e32601d2aa7185c25c20805")
		check(err)
	}

	configurationStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	auditStore, err := storage.NewSQLiteAuditStore(database)
	check(err)

	applier := &slowApplier{}
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
			Succeeded:       auditWords.Audit.Results.Succeeded,
			Failed:          auditWords.Audit.Results.Failed,
		},
	}
	server := &api.Server{
		Token: "fixture-token", Management: management, AuditWords: auditWords, Errors: errorCatalog,
		Operations: application.OperationService{Store: operationStore},
		Audit:      &application.AuditService{Store: auditStore, RetentionDays: auditWords.Audit.RetentionDays},
	}
	handler := api.WithPluginConfigurations(server.Handler(), service)

	inflight := &atomic.Int64{}
	peak := &atomic.Int64{}
	observeOverlap := func(next *atomic.Int64) {
		current := next.Add(1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				return
			}
		}
	}
	tracked := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		observeOverlap(inflight)
		defer inflight.Add(-1)
		handler.ServeHTTP(response, request)
	})

	// Recover runs once before any apply, exactly as bootstrap does before the
	// Management API starts listening.
	check(service.Recover(ctx))

	var readers sync.WaitGroup
	stopReaders := make(chan struct{})
	// Readers run for the whole workload so store reads and fencing checks
	// overlap every apply phase, not just the settled end state.
	for _, instanceID := range instanceIDs {
		readers.Add(1)
		go func(id string) {
			defer readers.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
				}
				_, _ = service.Current(ctx, id)
				_ = service.IsInstanceFenced(id)
			}
		}(instanceID)
	}

	var writers sync.WaitGroup
	for _, instanceID := range instanceIDs {
		writers.Add(1)
		go func(id string) {
			defer writers.Done()
			for round := 1; round <= roundsPerInstance; round++ {
				path := management.Paths.Plugins + "/" + id + management.Paths.PluginSettingsSuffix
				body := []byte(fmt.Sprintf(`{"instance":%q,"round":%d}`, id, round))
				key := fmt.Sprintf("race-%s-%02d-000000", id, round)
				applyRound(tracked, path, body, fmt.Sprintf(`"%d"`, round), key, management)
			}
		}(instanceID)
	}
	writers.Wait()
	close(stopReaders)
	readers.Wait()

	settled := time.Now().Add(settleTimeout)
	var succeeded int
	for time.Now().Before(settled) {
		succeeded = countSucceeded(ctx, database, management.Statuses.Succeeded)
		if succeeded == instanceCount*roundsPerInstance {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	var stagingLeftBehind int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_config_generations WHERE slot = ?`,
		pluginConfigurationWords.Slots.Staging).Scan(&stagingLeftBehind))
	rows, err := database.QueryContext(ctx,
		`SELECT instance_id, COUNT(*) FROM plugin_config_generations WHERE slot = ? GROUP BY instance_id`, "active")
	check(err)
	instancesWithExactlyOneActive := 0
	for rows.Next() {
		var instanceID string
		var activeCount int
		check(rows.Scan(&instanceID, &activeCount))
		if activeCount == 1 {
			instancesWithExactlyOneActive++
		}
	}
	check(rows.Err())
	check(rows.Close())

	instancesLeftFenced := 0
	for _, instanceID := range instanceIDs {
		if service.IsInstanceFenced(instanceID) {
			instancesLeftFenced++
		}
	}

	// Recovery on an already settled database must not replay anything: every
	// operation reached a terminal state, so a restart-time Recover is a no-op.
	beforeRecovery := applier.calls.Load()
	check(service.Recover(ctx))
	recoveryAfterSettleWasNoOp := applier.calls.Load() == beforeRecovery

	writeReport(map[string]any{
		"instances":                     instanceCount,
		"rounds":                        roundsPerInstance,
		"appliedOperations":             applier.calls.Load(),
		"succeededOperations":           succeeded,
		"concurrentRequestsObserved":    peak.Load() > 1,
		"recoveryAfterSettleWasNoOp":    recoveryAfterSettleWasNoOp,
		"instancesLeftFenced":           instancesLeftFenced,
		"stagingLeftBehind":             stagingLeftBehind,
		"instancesWithExactlyOneActive": instancesWithExactlyOneActive,
	})
}

// applyRound submits one configuration and waits for its operation to settle, so
// the next round observes the ETag the promotion just published.
func applyRound(handler http.Handler, path string, body []byte, ifMatch, key string, management config.ManagementWords) {
	request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture-token")
	request.Header.Set(management.Headers.ContentType, management.ContentTypes.JSON)
	request.Header.Set(management.Headers.IfMatch, ifMatch)
	request.Header.Set(management.Idempotency.Key, key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		panic(fmt.Sprintf("PUT %s returned %d: %s", path, response.Code, response.Body.String()))
	}
	var accepted map[string]any
	check(json.Unmarshal(response.Body.Bytes(), &accepted))
	operationID, _ := accepted[management.JSON.OperationID].(string)
	if operationID == "" {
		panic("accepted apply did not return an operation id")
	}
	waitForTerminal(handler, operationID, management)
}

func waitForTerminal(handler http.Handler, operationID string, management config.ManagementWords) {
	deadline := time.Now().Add(settleTimeout)
	for time.Now().Before(deadline) {
		request := httptest.NewRequest(http.MethodGet, management.Paths.Operations+"/"+operationID, nil)
		request.Header.Set("Authorization", "Bearer fixture-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var operation map[string]any
		check(json.Unmarshal(response.Body.Bytes(), &operation))
		state, _ := operation[management.JSON.State].(string)
		if state == management.Statuses.Succeeded || state == management.Statuses.Failed {
			if state != management.Statuses.Succeeded {
				panic("apply operation failed: " + response.Body.String())
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	panic("operation did not reach a terminal state")
}

func countSucceeded(ctx context.Context, database *sql.DB, succeeded string) int {
	var count int
	check(database.QueryRowContext(ctx, `SELECT COUNT(*) FROM operations WHERE state = ?`, succeeded).Scan(&count))
	return count
}

func writeReport(report map[string]any) {
	check(json.NewEncoder(os.Stdout).Encode(report))
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
