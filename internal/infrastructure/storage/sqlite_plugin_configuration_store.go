package storage

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

//go:embed sql/plugin_configuration.sql
var pluginConfigurationSQL embed.FS

type PluginConfigurationSQL struct{ queries map[string]string }

type pluginConfigurationStoreContract struct {
	SchemaVersion      int64  `yaml:"schemaVersion"`
	MaximumPayloadSize int    `yaml:"maximumPayloadBytes"`
	TimestampLayout    string `yaml:"timestampLayout"`
	MigrationActor     string `yaml:"migrationActor"`
	Slots              struct {
		Active   string `yaml:"active"`
		Previous string `yaml:"previous"`
		Staging  string `yaml:"staging"`
	} `yaml:"slots"`
	OperationStates struct {
		Pending string `yaml:"pending"`
		Running string `yaml:"running"`
	} `yaml:"operationStates"`
	Diagnostics struct {
		InvalidContract string `yaml:"invalidContract"`
		InvalidDocument string `yaml:"invalidDocument"`
		InvalidStore    string `yaml:"invalidStore"`
		MigrationFailed string `yaml:"migrationFailed"`
	} `yaml:"diagnostics"`
}

type SQLitePluginConfigurationStore struct {
	database *sql.DB
	queries  PluginConfigurationSQL
	contract pluginConfigurationStoreContract
	audit    *SQLiteAuditStore
}

var _ interfaces.PluginConfigurationStore = (*SQLitePluginConfigurationStore)(nil)

func NewSQLitePluginConfigurationStore(database *sql.DB) (*SQLitePluginConfigurationStore, error) {
	contents, err := assets.Contract(assets.SQLitePluginConfiguration)
	if err != nil {
		return nil, err
	}
	var contract pluginConfigurationStoreContract
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		return nil, err
	}
	queries, err := loadPluginConfigurationSQL(contract.Diagnostics.InvalidContract)
	if err != nil {
		return nil, err
	}
	if database == nil || !validPluginConfigurationContract(contract) {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	store := &SQLitePluginConfigurationStore{database: database, queries: queries, contract: contract, audit: audit}
	if err := store.migrateLegacyConfiguration(context.Background()); err != nil {
		return nil, err
	}
	var rebuild bool
	if err := database.QueryRowContext(context.Background(), queries.queries["operation-payload-needs-v8"]).Scan(&rebuild); err != nil {
		return nil, err
	}
	if rebuild {
		tx, err := database.BeginTx(context.Background(), nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(context.Background(), queries.queries["rebuild-operation-payloads-v8"]); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (store *SQLitePluginConfigurationStore) Current(ctx context.Context, instanceID string) (models.PluginConfigurationRevision, models.PluginConfigurationPointers, error) {
	if store == nil || store.database == nil || instanceID == "" {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, sql.ErrConnDone
	}
	query := store.queries.queries["select-current"]
	revision, pointers, err := scanCurrentConfiguration(store.database.QueryRowContext(ctx, query,
		store.contract.Slots.Previous, store.contract.Slots.Staging, instanceID, store.contract.Slots.Active), store.contract.TimestampLayout)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if checkErr := store.database.QueryRowContext(ctx, store.queries.queries["instance-exists"], instanceID).Scan(&exists); checkErr != nil {
			return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, checkErr
		}
		if !exists {
			return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, models.PluginConfigurationNotFound{}
		}
		return models.PluginConfigurationRevision{InstanceID: instanceID, SchemaVersion: store.contract.SchemaVersion},
			models.PluginConfigurationPointers{InstanceID: instanceID}, nil
	}
	return revision, pointers, err
}

func (store *SQLitePluginConfigurationStore) GetRevision(ctx context.Context, instanceID string, generation int64) (models.PluginConfigurationRevision, error) {
	if store == nil || store.database == nil {
		return models.PluginConfigurationRevision{}, sql.ErrConnDone
	}
	if instanceID == "" || generation < 1 {
		return models.PluginConfigurationRevision{}, errors.New(store.contract.Diagnostics.InvalidDocument)
	}
	revision, err := scanConfigurationRevision(store.database.QueryRowContext(ctx,
		store.queries.queries["select-generation"], instanceID, generation), store.contract.TimestampLayout)
	if errors.Is(err, sql.ErrNoRows) {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationNotFound{}
	}
	return revision, err
}

func (store *SQLitePluginConfigurationStore) CreateCandidate(ctx context.Context, operationID, operationKind, operationState, instanceID string, expectedCurrent, schemaVersion int64, rawJSON []byte, audit models.AuditRecord) (models.PluginConfigurationRevision, error) {
	if store == nil || store.database == nil {
		return models.PluginConfigurationRevision{}, sql.ErrConnDone
	}
	if instanceID == "" || len(rawJSON) > store.contract.MaximumPayloadSize || !validPluginConfiguration(rawJSON) || schemaVersion < 1 || !validConfigurationAudit(audit) {
		return models.PluginConfigurationRevision{}, errors.New(store.contract.Diagnostics.InvalidDocument)
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	defer tx.Rollback()
	pointers, err := readConfigurationPointers(ctx, tx, store.queries, store.contract, instanceID)
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	if pointers.CurrentRevision != expectedCurrent || pointers.PendingRevision != 0 {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationConflict{}
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, store.queries.queries["next-generation"], instanceID).Scan(&generation); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	digest := configurationDigest(rawJSON)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, store.queries.queries["insert-generation"], instanceID, generation, store.contract.Slots.Staging,
		rawJSON, digest, schemaVersion, now.Format(store.contract.TimestampLayout)); err != nil {
		if isUniqueConstraint(err) {
			return models.PluginConfigurationRevision{}, models.PluginConfigurationConflict{}
		}
		return models.PluginConfigurationRevision{}, err
	}
	if operationID != "" {
		if operationKind == "" || operationState == "" {
			return models.PluginConfigurationRevision{}, models.PluginConfigurationConflict{}
		}
		linked, err := tx.ExecContext(ctx, store.queries.queries["set-operation-candidate"], generation, operationID, operationKind, instanceID, operationState)
		if err != nil {
			return models.PluginConfigurationRevision{}, err
		}
		if err := requireOneRow(linked); err != nil {
			return models.PluginConfigurationRevision{}, models.PluginConfigurationConflict{}
		}
	}
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	return models.PluginConfigurationRevision{
		InstanceID: instanceID, Revision: generation, SchemaVersion: schemaVersion, Digest: digest,
		SettingsJSON: append([]byte(nil), rawJSON...), State: store.contract.Slots.Staging, Actor: audit.Actor, CreatedAt: now,
	}, nil
}

func (store *SQLitePluginConfigurationStore) ActivateCandidate(ctx context.Context, instanceID string, generation, expectedCurrent int64, audit models.AuditRecord) (models.PluginConfigurationPointers, error) {
	return store.transitionCandidate(ctx, instanceID, generation, expectedCurrent, true, audit)
}

func (store *SQLitePluginConfigurationStore) FailCandidate(ctx context.Context, instanceID string, generation, expectedCurrent int64, audit models.AuditRecord) (models.PluginConfigurationPointers, error) {
	return store.transitionCandidate(ctx, instanceID, generation, expectedCurrent, false, audit)
}

func (store *SQLitePluginConfigurationStore) transitionCandidate(ctx context.Context, instanceID string, generation, expectedCurrent int64, activate bool, audit models.AuditRecord) (models.PluginConfigurationPointers, error) {
	if store == nil || store.database == nil {
		return models.PluginConfigurationPointers{}, sql.ErrConnDone
	}
	if instanceID == "" || generation < 1 || !validConfigurationAudit(audit) {
		return models.PluginConfigurationPointers{}, errors.New(store.contract.Diagnostics.InvalidDocument)
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	defer tx.Rollback()
	pointers, err := readConfigurationPointers(ctx, tx, store.queries, store.contract, instanceID)
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if pointers.CurrentRevision != expectedCurrent || pointers.PendingRevision != generation {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationConflict{}
	}
	candidate, err := scanConfigurationRevision(tx.QueryRowContext(ctx, store.queries.queries["select-generation"], instanceID, generation), store.contract.TimestampLayout)
	if errors.Is(err, sql.ErrNoRows) {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationNotFound{}
	}
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if candidate.State != store.contract.Slots.Staging {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationConflict{}
	}
	if activate {
		if _, err := tx.ExecContext(ctx, store.queries.queries["delete-slot"], instanceID, store.contract.Slots.Previous); err != nil {
			return models.PluginConfigurationPointers{}, err
		}
		if pointers.CurrentRevision > 0 {
			if err := moveConfigurationSlot(ctx, tx, store, instanceID, pointers.CurrentRevision, store.contract.Slots.Active, store.contract.Slots.Previous); err != nil {
				return models.PluginConfigurationPointers{}, err
			}
		}
		if err := moveConfigurationSlot(ctx, tx, store, instanceID, generation, store.contract.Slots.Staging, store.contract.Slots.Active); err != nil {
			return models.PluginConfigurationPointers{}, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, store.queries.queries["delete-generation"], instanceID, generation, store.contract.Slots.Staging); err != nil {
			return models.PluginConfigurationPointers{}, err
		}
		var remaining int
		if err := tx.QueryRowContext(ctx, store.queries.queries["count-generation"], instanceID, generation).Scan(&remaining); err != nil {
			return models.PluginConfigurationPointers{}, err
		}
		if remaining != 0 {
			return models.PluginConfigurationPointers{}, models.PluginConfigurationConflict{}
		}
	}
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	return readConfigurationPointers(ctx, store.database, store.queries, store.contract, instanceID)
}

func (store *SQLitePluginConfigurationStore) RestorePrevious(ctx context.Context, instanceID string, expectedCurrent int64, audit models.AuditRecord) (models.PluginConfigurationPointers, error) {
	if store == nil || store.database == nil {
		return models.PluginConfigurationPointers{}, sql.ErrConnDone
	}
	if instanceID == "" || !validConfigurationAudit(audit) {
		return models.PluginConfigurationPointers{}, errors.New(store.contract.Diagnostics.InvalidDocument)
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	defer tx.Rollback()
	pointers, err := readConfigurationPointers(ctx, tx, store.queries, store.contract, instanceID)
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if pointers.CurrentRevision != expectedCurrent || pointers.PreviousRevision == 0 {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationConflict{}
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, store.queries.queries["delete-slot"], instanceID, store.contract.Slots.Staging); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := moveConfigurationSlot(ctx, tx, store, instanceID, pointers.CurrentRevision, store.contract.Slots.Active, store.contract.Slots.Staging); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := moveConfigurationSlot(ctx, tx, store, instanceID, pointers.PreviousRevision, store.contract.Slots.Previous, store.contract.Slots.Active); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := moveConfigurationSlot(ctx, tx, store, instanceID, pointers.CurrentRevision, store.contract.Slots.Staging, store.contract.Slots.Previous); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	audit.Timestamp = now
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	return models.PluginConfigurationPointers{InstanceID: instanceID, CurrentRevision: pointers.PreviousRevision, PreviousRevision: pointers.CurrentRevision}, nil
}

func (store *SQLitePluginConfigurationStore) migrateLegacyConfiguration(ctx context.Context) error {
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	legacyRevisions, err := tableExists(ctx, tx, store.queries, "plugin_config_revisions")
	if err != nil {
		return err
	}
	legacyPointers, err := tableExists(ctx, tx, store.queries, "plugin_config_pointers")
	if err != nil {
		return err
	}
	if legacyRevisions != legacyPointers {
		return errors.New(store.contract.Diagnostics.MigrationFailed)
	}
	if legacyPointers {
		var corrupt int
		if err := tx.QueryRowContext(ctx, store.queries.queries["legacy-configuration-integrity"]).Scan(&corrupt); err != nil || corrupt != 0 {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
		rows, err := tx.QueryContext(ctx, store.queries.queries["legacy-configurations"],
			store.contract.Slots.Active, store.contract.Slots.Previous, store.contract.Slots.Staging)
		if err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
		for rows.Next() {
			var instanceID, digest, createdAt, slot string
			var raw []byte
			var generation, schemaVersion int64
			if err := rows.Scan(&instanceID, &generation, &schemaVersion, &digest, &raw, &createdAt, &slot); err != nil {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
			if schemaVersion < 1 || generation < 1 || !validPluginConfiguration(raw) || configurationDigest(raw) != digest || !validConfigurationSlot(store.contract, slot) {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
			if err := insertMigratedGeneration(ctx, tx, store, instanceID, generation, slot, raw, digest, schemaVersion, createdAt); err != nil {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
		if err := rows.Close(); err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
	}
	if legacyPointers {
		var invalidSlots int
		if err := tx.QueryRowContext(ctx, store.queries.queries["legacy-configuration-integrity"]).Scan(&invalidSlots); err != nil || invalidSlots != 0 {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
	}
	legacySettings, err := columnExists(ctx, tx, store.queries, "plugin_instances", "settings_json")
	if err != nil {
		return err
	}
	legacyRevision, err := columnExists(ctx, tx, store.queries, "plugin_instances", "revision")
	if err != nil {
		return err
	}
	if legacySettings != legacyRevision {
		return errors.New(store.contract.Diagnostics.MigrationFailed)
	}
	if legacySettings {
		rows, err := tx.QueryContext(ctx, store.queries.queries["list-legacy-instance-configurations"])
		if err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
		for rows.Next() {
			var instanceID string
			var generation int64
			var raw []byte
			if err := rows.Scan(&instanceID, &generation, &raw); err != nil {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
			var active int
			if err := tx.QueryRowContext(ctx, store.queries.queries["count-slot"], instanceID, store.contract.Slots.Active).Scan(&active); err != nil {
				_ = rows.Close()
				return err
			}
			if active == 0 {
				if generation < 1 || !validPluginConfiguration(raw) {
					_ = rows.Close()
					return errors.New(store.contract.Diagnostics.MigrationFailed)
				}
				if err := insertMigratedGeneration(ctx, tx, store, instanceID, generation, store.contract.Slots.Active, raw,
					configurationDigest(raw), store.contract.SchemaVersion, time.Now().UTC().Format(store.contract.TimestampLayout)); err != nil {
					_ = rows.Close()
					return errors.New(store.contract.Diagnostics.MigrationFailed)
				}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
		if err := rows.Close(); err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
	}
	legacyPayloadSettings, err := columnExists(ctx, tx, store.queries, "operation_payloads", "settings_json")
	if err != nil {
		return err
	}
	if legacyPayloadSettings {
		rows, err := tx.QueryContext(ctx, store.queries.queries["legacy-operation-configurations"])
		if err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
		for rows.Next() {
			var operationID, resource, requestDigest, state string
			var resultText sql.NullString
			var version, expected, schemaVersion int64
			var raw []byte
			if err := rows.Scan(&operationID, &version, &resource, &expected, &schemaVersion, &requestDigest, &raw, &resultText, &state); err != nil {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
			if state != store.contract.OperationStates.Pending && state != store.contract.OperationStates.Running {
				continue
			}
			if version < 1 || expected < 1 || schemaVersion < 1 || requestDigest == "" || !validPluginConfiguration(raw) {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
			if resultText.Valid && resultText.String != "" {
				continue
			}
			pointers, err := readConfigurationPointers(ctx, tx, store.queries, store.contract, resource)
			if err != nil || pointers.PendingRevision != 0 {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
			var generation int64
			if err := tx.QueryRowContext(ctx, store.queries.queries["next-generation"], resource).Scan(&generation); err != nil {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
			if err := insertMigratedGeneration(ctx, tx, store, resource, generation, store.contract.Slots.Staging, raw,
				configurationDigest(raw), schemaVersion, time.Now().UTC().Format(store.contract.TimestampLayout)); err != nil {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
			if _, err := tx.ExecContext(ctx, store.queries.queries["set-operation-generation"], generation, operationID); err != nil {
				_ = rows.Close()
				return errors.New(store.contract.Diagnostics.MigrationFailed)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
		if err := rows.Close(); err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
	}
	if legacyPointers {
		if _, err := tx.ExecContext(ctx, store.queries.queries["drop-legacy-pointers"]); err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
		if _, err := tx.ExecContext(ctx, store.queries.queries["drop-legacy-revisions"]); err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
	}
	if legacySettings {
		if _, err := tx.ExecContext(ctx, store.queries.queries["drop-legacy-instance-settings"]); err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
		if _, err := tx.ExecContext(ctx, store.queries.queries["drop-legacy-instance-revision"]); err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
	}
	if legacyPayloadSettings {
		if _, err := tx.ExecContext(ctx, store.queries.queries["drop-legacy-operation-settings"]); err != nil {
			return errors.New(store.contract.Diagnostics.MigrationFailed)
		}
	}
	return tx.Commit()
}

func loadPluginConfigurationSQL(invalidContract string) (PluginConfigurationSQL, error) {
	contents, err := pluginConfigurationSQL.ReadFile("sql/plugin_configuration.sql")
	if err != nil {
		return PluginConfigurationSQL{}, errors.New(invalidContract)
	}
	queries := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	name := ""
	var statement strings.Builder
	storeStatement := func() bool {
		if name == "" {
			return true
		}
		if _, exists := queries[name]; exists || strings.TrimSpace(statement.String()) == "" {
			return false
		}
		queries[name] = strings.TrimSpace(statement.String())
		return true
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "-- name: ") {
			if !storeStatement() {
				return PluginConfigurationSQL{}, errors.New(invalidContract)
			}
			name = strings.TrimSpace(strings.TrimPrefix(line, "-- name: "))
			statement.Reset()
			continue
		}
		if name != "" {
			statement.WriteString(line)
			statement.WriteByte('\n')
		}
	}
	if scanner.Err() != nil || !storeStatement() || len(queries) != 26 {
		return PluginConfigurationSQL{}, errors.New(invalidContract)
	}
	return PluginConfigurationSQL{queries: queries}, nil
}

func scanCurrentConfiguration(row *sql.Row, timestampLayout string) (models.PluginConfigurationRevision, models.PluginConfigurationPointers, error) {
	var revision models.PluginConfigurationRevision
	var createdAt string
	var active, previous, staging int64
	if err := row.Scan(&revision.InstanceID, &revision.Revision, &revision.SchemaVersion, &revision.Digest, &revision.SettingsJSON,
		&revision.State, &createdAt, &active, &previous, &staging); err != nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, err
	}
	parsed, err := time.Parse(timestampLayout, createdAt)
	if err != nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, err
	}
	revision.CreatedAt = parsed
	pointers := models.PluginConfigurationPointers{InstanceID: revision.InstanceID, CurrentRevision: active, PreviousRevision: previous, PendingRevision: staging}
	return revision, pointers, nil
}

func scanConfigurationRevision(row *sql.Row, timestampLayout string) (models.PluginConfigurationRevision, error) {
	var revision models.PluginConfigurationRevision
	var createdAt string
	if err := row.Scan(&revision.InstanceID, &revision.Revision, &revision.SchemaVersion, &revision.Digest, &revision.SettingsJSON, &revision.State, &createdAt); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	parsed, err := time.Parse(timestampLayout, createdAt)
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	revision.CreatedAt = parsed
	return revision, nil
}

func readConfigurationPointers(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, queries PluginConfigurationSQL, contract pluginConfigurationStoreContract, instanceID string) (models.PluginConfigurationPointers, error) {
	pointers := models.PluginConfigurationPointers{InstanceID: instanceID}
	rows, err := queryer.QueryContext(ctx, queries.queries["select-slots"], instanceID)
	if err != nil {
		return pointers, err
	}
	defer rows.Close()
	for rows.Next() {
		var slot string
		var generation int64
		if err := rows.Scan(&slot, &generation); err != nil {
			return pointers, err
		}
		switch slot {
		case contract.Slots.Active:
			pointers.CurrentRevision = generation
		case contract.Slots.Previous:
			pointers.PreviousRevision = generation
		case contract.Slots.Staging:
			pointers.PendingRevision = generation
		default:
			return pointers, sql.ErrNoRows
		}
	}
	return pointers, rows.Err()
}

func moveConfigurationSlot(ctx context.Context, tx *sql.Tx, store *SQLitePluginConfigurationStore, instanceID string, generation int64, from, to string) error {
	result, err := tx.ExecContext(ctx, store.queries.queries["move-generation-slot"], to, instanceID, generation, from)
	if err != nil {
		return err
	}
	return requireOneRow(result)
}

func insertMigratedGeneration(ctx context.Context, tx *sql.Tx, store *SQLitePluginConfigurationStore, instanceID string, generation int64, slot string, raw []byte, digest string, schemaVersion int64, createdAt string) error {
	if instanceID == "" || generation < 1 || !validConfigurationSlot(store.contract, slot) || schemaVersion < 1 || !validPluginConfiguration(raw) || configurationDigest(raw) != digest {
		return errors.New(store.contract.Diagnostics.MigrationFailed)
	}
	_, err := tx.ExecContext(ctx, store.queries.queries["insert-generation"], instanceID, generation, slot, raw, digest, schemaVersion, createdAt)
	return err
}

func tableExists(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, queries PluginConfigurationSQL, table string) (bool, error) {
	var found bool
	err := queryer.QueryRowContext(ctx, queries.queries["table-exists"], table).Scan(&found)
	return found, err
}

func columnExists(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, queries PluginConfigurationSQL, table, column string) (bool, error) {
	var found bool
	err := queryer.QueryRowContext(ctx, queries.queries["column-exists"], table, column).Scan(&found)
	return found, err
}

func validPluginConfiguration(raw []byte) bool {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil {
		return false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return false
	}
	object, ok := value.(map[string]any)
	return ok && object != nil
}

func decodeUniqueJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return token, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("invalid json object key")
			}
			if _, exists := object[key]; exists {
				return nil, errors.New("duplicate json object key")
			}
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		_, err := decoder.Token()
		return object, err
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueJSONValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err := decoder.Token()
		return array, err
	default:
		return nil, errors.New("invalid json composite")
	}
}

func validConfigurationAudit(audit models.AuditRecord) bool {
	return audit.Actor != "" && audit.Action != "" && audit.Resource != "" && audit.Result != "" && audit.RequestID != ""
}

func configurationDigest(document []byte) string {
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:])
}

func validConfigurationSlot(contract pluginConfigurationStoreContract, candidate string) bool {
	return candidate != "" && (candidate == contract.Slots.Active || candidate == contract.Slots.Previous || candidate == contract.Slots.Staging)
}

func validPluginConfigurationContract(contract pluginConfigurationStoreContract) bool {
	slots := []string{contract.Slots.Active, contract.Slots.Previous, contract.Slots.Staging}
	for index, slot := range slots {
		if slot == "" || contains(slots[:index], slot) {
			return false
		}
	}
	return contract.SchemaVersion > 0 && contract.MaximumPayloadSize > 0 && contract.TimestampLayout != "" &&
		contract.MigrationActor != "" && contract.OperationStates.Pending != "" && contract.OperationStates.Running != "" &&
		contract.Diagnostics.InvalidContract != "" && contract.Diagnostics.InvalidDocument != "" &&
		contract.Diagnostics.InvalidStore != "" && contract.Diagnostics.MigrationFailed != ""
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func requireOneRow(result sql.Result) error {
	if result == nil {
		return io.ErrUnexpectedEOF
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}
