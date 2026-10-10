package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
	"github.com/Liapoldus/core/v3/internal/infrastructure/config"
	"github.com/Liapoldus/core/v3/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/v3/internal/infrastructure/storage"
	sdkmodels "github.com/Liapoldus/plugin-sdk/v2/domain/models"
)

const (
	instanceID   = "forms"
	activeRaw    = "{ \"release\" : \"stable\" }\n"
	previousRaw  = "{\n  \"release\" : \"older\"\n}\n"
	candidateRaw = "{ \"release\" : \"candidate\" }\n"
)

type replicaSource struct{ replicas []plugins.LivePluginReplica }

func (source replicaSource) Snapshot() []plugins.LivePluginReplica { return source.replicas }
func (replicaSource) HasRegisteredInstance(id string) bool         { return id == instanceID }

type observedApplier struct {
	*plugins.SDKConfigurationApplier
	calls int
}

func (applier *observedApplier) ApplyConfiguration(context.Context, string, string, []byte) error {
	applier.calls++
	return nil
}

func main() {
	if len(os.Args) != 2 {
		panic("expected SQLite database path")
	}
	ctx := context.Background()
	management, err := config.LoadManagement()
	check(err)
	pluginConfiguration, err := config.LoadPluginConfiguration()
	check(err)
	sqliteContract, err := config.LoadSQLiteContract()
	check(err)
	database, err := storage.OpenSQLite(ctx, os.Args[1], storage.SQLiteOptions{
		Driver: sqliteContract.Driver, ParentDirectoryMode: sqliteContract.ParentDirectoryMode,
		DatabaseFileMode: sqliteContract.DatabaseFileMode, MaxOpenConnections: sqliteContract.MaxOpenConnections,
		MaxIdleConnections: sqliteContract.MaxIdleConnections, SchemaVersion: sqliteContract.SchemaVersion,
		HasMigrationTableQuery: sqliteContract.HasMigrationTableQuery, MigrationVersionQuery: sqliteContract.MigrationVersionQuery,
		SchemaVersionError: sqliteContract.SchemaVersionError, Pragmas: sqliteContract.Pragmas,
	}, sqliteContract.Schema)
	check(err)
	defer database.Close()
	seed(ctx, database)
	configurationStore, err := storage.NewSQLitePluginConfigurationStore(database)
	check(err)
	operationStore, err := storage.NewSQLiteOperationStore(database)
	check(err)
	now := time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	resolver := plugins.NewRegisteredReplicaReloadResolver(replicaSource{replicas: incompatibleReplicas(now)}, nil, func() time.Time { return now })
	pluginApplier := &observedApplier{SDKConfigurationApplier: &plugins.SDKConfigurationApplier{Registered: resolver}}
	service := &application.PluginConfigurationService{
		Store: configurationStore, Applier: pluginApplier,
		Operations:    application.OperationService{Store: operationStore},
		OperationKind: management.OperationKinds.PluginSettingsApply,
		OperationStates: application.PluginConfigurationOperationStates{
			Pending: management.Statuses.Pending, Running: management.Statuses.Running,
			Succeeded: management.Statuses.Succeeded, Failed: management.Statuses.Failed,
		},
		OperationFailureCodes: application.PluginConfigurationFailureCodes{
			Conflict: management.Codes.PluginRevisionConflict, ApplyFailed: management.Codes.ActivationFailed,
		},
		RevisionStates: application.PluginConfigurationRevisionStates{
			Active: pluginConfiguration.Slots.Active, Candidate: pluginConfiguration.Slots.Staging,
		},
		PayloadVersion:      pluginConfiguration.SchemaVersion,
		MaximumPayloadBytes: pluginConfiguration.MaximumPayloadBytes,
		ScheduleWorker:      func(worker func()) { worker() },
	}
	raw := []byte(candidateRaw)
	payload := models.OperationPayload{
		Version: pluginConfiguration.SchemaVersion, Resource: instanceID,
		ExpectedRevision: 2, SchemaVersion: pluginConfiguration.SchemaVersion,
	}
	payload.Digest = payload.ComputeDigest(raw)
	operation, err := service.Submit(ctx, application.ApplyPluginConfigurationCommand{
		InstanceID: instanceID, ExpectedRevision: 2, SchemaVersion: pluginConfiguration.SchemaVersion,
		SettingsJSON:   raw,
		CandidateAudit: models.AuditRecord{Actor: "operator", Action: "candidate", Result: "succeeded", RequestID: "cohort-activation"},
		AppliedAudit:   models.AuditRecord{Actor: "operator", Action: "apply", Result: "succeeded", RequestID: "cohort-activation"},
		FailedAudit:    models.AuditRecord{Actor: "operator", Action: "apply-failed", Result: "failed", RequestID: "cohort-activation"},
	}, models.OperationReservation{
		Operation: models.Operation{ID: "operation-cohort-activation", Kind: management.OperationKinds.PluginSettingsApply,
			State: management.Statuses.Pending, RequestID: "cohort-activation", Actor: "operator", Resource: instanceID, CreatedAt: now},
		Scope: "plugin-settings/forms", Key: "release-cohort-key", RequestDigest: payload.Digest, Payload: &payload,
	})
	check(err)
	finalOperation, err := operationStore.Get(ctx, operation.ID)
	check(err)
	active, pointers, err := configurationStore.Current(ctx, instanceID)
	check(err)
	previous, previousExists := readSlot(ctx, database, pluginConfiguration.Slots.Previous)
	_, stagingExists := readSlot(ctx, database, pluginConfiguration.Slots.Staging)
	if !previousExists {
		panic("previous configuration generation disappeared")
	}
	write(map[string]any{
		"operationState": finalOperation.State, "operationErrorCode": finalOperation.ErrorCode,
		"activeGeneration": pointers.CurrentRevision, "activeRaw": string(active.SettingsJSON),
		"previousGeneration": pointers.PreviousRevision, "previousRaw": previous,
		"stagingPresent": stagingExists, "pluginApplyCalls": pluginApplier.calls,
	})
}

func seed(ctx context.Context, database *sql.DB) {
	if _, err := database.ExecContext(ctx,
		`INSERT INTO plugin_instances (id, manifest_json, state) VALUES (?, ?, ?)`, instanceID, []byte(`{"name":"forms"}`), "configured"); err != nil {
		panic(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO plugin_registered_instances(instance_id) VALUES (?)`, instanceID); err != nil {
		panic(err)
	}
	insertGeneration(ctx, database, 1, "previous", previousRaw)
	insertGeneration(ctx, database, 2, "active", activeRaw)
}

func insertGeneration(ctx context.Context, database *sql.DB, generation int64, slot, raw string) {
	digest := sha256.Sum256([]byte(raw))
	if _, err := database.ExecContext(ctx,
		`INSERT INTO plugin_config_generations (instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
		 VALUES (?, ?, ?, ?, ?, 1, ?)`, instanceID, generation, slot, []byte(raw), hex.EncodeToString(digest[:]), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		panic(err)
	}
}

func readSlot(ctx context.Context, database *sql.DB, slot string) (string, bool) {
	var raw []byte
	err := database.QueryRowContext(ctx, `SELECT raw_json FROM plugin_config_generations WHERE instance_id = ? AND slot = ?`, instanceID, slot).Scan(&raw)
	if err == sql.ErrNoRows {
		return "", false
	}
	check(err)
	return string(raw), true
}

func incompatibleReplicas(now time.Time) []plugins.LivePluginReplica {
	left := registration("replica-a", "release-a", "1.0.0", "1.1.0")
	right := registration("replica-b", "release-b", "2.0.0", "2.0.0")
	return []plugins.LivePluginReplica{
		{Registration: left, LeaseExpires: now.Add(time.Minute)},
		{Registration: right, LeaseExpires: now.Add(time.Minute)},
	}
}

func registration(replicaID, release, advertised, maximum string) sdkmodels.ReplicaRegistrationRequest {
	contractDigest := sha256.Sum256([]byte("settings-schema-" + advertised))
	releaseDigest := sha256.Sum256([]byte(release))
	return sdkmodels.ReplicaRegistrationRequest{
		ContractVersion: "liapoldus.plugin-sdk.replica-lifecycle.v2",
		Identity:        sdkmodels.PeerReplicaID{InstanceID: instanceID, ReplicaID: replicaID, IncarnationID: "inc-" + replicaID, PlacementID: "zone-a"},
		RestEndpoint:    "https://127.0.0.1:9443", PeerEndpoints: []sdkmodels.ReplicaPeerEndpoint{},
		Release:             sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: fmt.Sprintf("%x", releaseDigest[:])},
		AdvertisedContracts: []sdkmodels.ContractVersion{{ContractID: "org.example.settings-schema", Version: advertised, SHA256: fmt.Sprintf("%x", contractDigest[:])}},
		AcceptedContracts:   []sdkmodels.ContractRange{{ContractID: "org.example.settings-schema", MinimumVersion: "1.0.0", MaximumVersionExclusive: maximum}},
	}
}

func write(value any) { check(json.NewEncoder(os.Stdout).Encode(value)) }
func check(err error) {
	if err != nil {
		panic(err)
	}
}
