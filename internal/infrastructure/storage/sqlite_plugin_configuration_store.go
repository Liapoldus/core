package storage

import (
	"bufio"
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

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

//go:embed sql/plugin_configuration.sql
var pluginConfigurationSQL embed.FS

type PluginConfigurationSQL struct {
	queries map[string]string
}

type pluginConfigurationStoreContract struct {
	SchemaVersion   int64  `yaml:"schemaVersion"`
	TimestampLayout string `yaml:"timestampLayout"`
	MigrationActor  string `yaml:"migrationActor"`
	States          struct {
		Active     string `yaml:"active"`
		Candidate  string `yaml:"candidate"`
		Superseded string `yaml:"superseded"`
		Failed     string `yaml:"failed"`
	} `yaml:"states"`
	Diagnostics struct {
		InvalidContract string `yaml:"invalidContract"`
		InvalidDocument string `yaml:"invalidDocument"`
		InvalidStore    string `yaml:"invalidStore"`
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
	if !validPluginConfigurationContract(contract) {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	if database == nil {
		return nil, errors.New(contract.Diagnostics.InvalidStore)
	}
	queries, err := loadPluginConfigurationSQL(contract.Diagnostics.InvalidContract)
	if err != nil {
		return nil, err
	}
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	store := &SQLitePluginConfigurationStore{database: database, queries: queries, contract: contract, audit: audit}
	if err := store.backfillCurrentConfigurations(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (store *SQLitePluginConfigurationStore) Current(ctx context.Context, instanceID string) (models.PluginConfigurationRevision, models.PluginConfigurationPointers, error) {
	if store == nil || store.database == nil || instanceID == "" {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, sql.ErrConnDone
	}
	revision, pointers, err := scanCurrentConfiguration(store.database.QueryRowContext(ctx, store.queries.queries["select-current"], instanceID), store.contract.TimestampLayout)
	if errors.Is(err, sql.ErrNoRows) {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, models.PluginConfigurationNotFound{}
	}
	return revision, pointers, err
}

func (store *SQLitePluginConfigurationStore) CreateCandidate(ctx context.Context, instanceID string, expectedCurrent, schemaVersion int64, settingsJSON []byte, audit models.AuditRecord) (models.PluginConfigurationRevision, error) {
	if store == nil || store.database == nil {
		return models.PluginConfigurationRevision{}, sql.ErrConnDone
	}
	if instanceID == "" || !validPluginConfiguration(settingsJSON) || schemaVersion < 1 || !validConfigurationAudit(audit) {
		return models.PluginConfigurationRevision{}, errors.New(store.contract.Diagnostics.InvalidDocument)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	defer transaction.Rollback()
	pointers, err := readConfigurationPointers(ctx, transaction, store.queries, instanceID)
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	if pointers.CurrentRevision != expectedCurrent || pointers.PendingRevision != 0 {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationConflict{}
	}
	var revisionID int64
	if err := transaction.QueryRowContext(ctx, store.queries.queries["next-revision"], instanceID).Scan(&revisionID); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	digest := configurationDigest(settingsJSON)
	now := time.Now().UTC()
	if _, err := transaction.ExecContext(ctx, store.queries.queries["insert-candidate"], instanceID, revisionID, schemaVersion, digest, settingsJSON, store.contract.States.Candidate, audit.Actor, now.Format(store.contract.TimestampLayout)); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	result, err := transaction.ExecContext(ctx, store.queries.queries["set-pending"], revisionID, now.Format(store.contract.TimestampLayout), instanceID, nullableRevision(expectedCurrent))
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	if err := requireOneRow(result); err != nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationConflict{}
	}
	if err := store.audit.append(ctx, transaction, audit); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	if err := transaction.Commit(); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	return models.PluginConfigurationRevision{
		InstanceID: instanceID, Revision: revisionID, SchemaVersion: schemaVersion,
		Digest: digest, SettingsJSON: append([]byte(nil), settingsJSON...), State: store.contract.States.Candidate,
		Actor: audit.Actor, CreatedAt: now,
	}, nil
}

func (store *SQLitePluginConfigurationStore) ActivateCandidate(ctx context.Context, instanceID string, candidateRevision, expectedCurrent int64, audit models.AuditRecord) (models.PluginConfigurationPointers, error) {
	return store.transitionCandidate(ctx, instanceID, candidateRevision, expectedCurrent, true, audit)
}

func (store *SQLitePluginConfigurationStore) FailCandidate(ctx context.Context, instanceID string, candidateRevision, expectedCurrent int64, audit models.AuditRecord) (models.PluginConfigurationPointers, error) {
	return store.transitionCandidate(ctx, instanceID, candidateRevision, expectedCurrent, false, audit)
}

func (store *SQLitePluginConfigurationStore) transitionCandidate(ctx context.Context, instanceID string, candidateRevision, expectedCurrent int64, activate bool, audit models.AuditRecord) (models.PluginConfigurationPointers, error) {
	if store == nil || store.database == nil {
		return models.PluginConfigurationPointers{}, sql.ErrConnDone
	}
	if instanceID == "" || candidateRevision < 1 || !validConfigurationAudit(audit) {
		return models.PluginConfigurationPointers{}, errors.New(store.contract.Diagnostics.InvalidDocument)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	defer transaction.Rollback()
	pointers, err := readConfigurationPointers(ctx, transaction, store.queries, instanceID)
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if pointers.CurrentRevision != expectedCurrent || pointers.PendingRevision != candidateRevision {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationConflict{}
	}
	candidate, err := scanConfigurationRevision(transaction.QueryRowContext(ctx, store.queries.queries["select-revision"], instanceID, candidateRevision), store.contract.TimestampLayout)
	if errors.Is(err, sql.ErrNoRows) {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationNotFound{}
	}
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if candidate.State != store.contract.States.Candidate {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationConflict{}
	}
	now := time.Now().UTC()
	if activate {
		if pointers.CurrentRevision > 0 {
			if err := setConfigurationRevisionState(ctx, transaction, store.queries, instanceID, pointers.CurrentRevision, store.contract.States.Active, store.contract.States.Superseded); err != nil {
				return models.PluginConfigurationPointers{}, err
			}
		}
		if err := setConfigurationRevisionState(ctx, transaction, store.queries, instanceID, candidateRevision, store.contract.States.Candidate, store.contract.States.Active); err != nil {
			return models.PluginConfigurationPointers{}, err
		}
		if err := updatePluginInstanceSettings(ctx, transaction, store.queries, instanceID, candidateRevision, candidate.SettingsJSON, now, store.contract.TimestampLayout); err != nil {
			return models.PluginConfigurationPointers{}, err
		}
	}
	if !activate {
		if err := setConfigurationRevisionState(ctx, transaction, store.queries, instanceID, candidateRevision, store.contract.States.Candidate, store.contract.States.Failed); err != nil {
			return models.PluginConfigurationPointers{}, err
		}
	}
	current := pointers.CurrentRevision
	previous := pointers.PreviousRevision
	if activate {
		previous = current
		current = candidateRevision
	}
	if err := updateConfigurationPointers(ctx, transaction, store.queries, instanceID, current, previous, 0, now, store.contract.TimestampLayout); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := store.audit.append(ctx, transaction, audit); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := transaction.Commit(); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	pointers.PreviousRevision = previous
	pointers.CurrentRevision = current
	pointers.PendingRevision = 0
	return pointers, nil
}

func (store *SQLitePluginConfigurationStore) RestorePrevious(ctx context.Context, instanceID string, expectedCurrent int64, audit models.AuditRecord) (models.PluginConfigurationPointers, error) {
	if store == nil || store.database == nil {
		return models.PluginConfigurationPointers{}, sql.ErrConnDone
	}
	if instanceID == "" || !validConfigurationAudit(audit) {
		return models.PluginConfigurationPointers{}, errors.New(store.contract.Diagnostics.InvalidDocument)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	defer transaction.Rollback()
	pointers, err := readConfigurationPointers(ctx, transaction, store.queries, instanceID)
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if pointers.CurrentRevision != expectedCurrent || pointers.PendingRevision != 0 {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationConflict{}
	}
	if pointers.PreviousRevision == 0 {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationNotFound{}
	}
	previous, err := scanConfigurationRevision(transaction.QueryRowContext(ctx, store.queries.queries["select-revision"], instanceID, pointers.PreviousRevision), store.contract.TimestampLayout)
	if errors.Is(err, sql.ErrNoRows) {
		return models.PluginConfigurationPointers{}, models.PluginConfigurationNotFound{}
	}
	if err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	now := time.Now().UTC()
	if err := setConfigurationRevisionState(ctx, transaction, store.queries, instanceID, pointers.CurrentRevision, store.contract.States.Active, store.contract.States.Superseded); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := setConfigurationRevisionState(ctx, transaction, store.queries, instanceID, pointers.PreviousRevision, store.contract.States.Superseded, store.contract.States.Active); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := updatePluginInstanceSettings(ctx, transaction, store.queries, instanceID, previous.Revision, previous.SettingsJSON, now, store.contract.TimestampLayout); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := updateConfigurationPointers(ctx, transaction, store.queries, instanceID, pointers.PreviousRevision, pointers.CurrentRevision, 0, now, store.contract.TimestampLayout); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := store.audit.append(ctx, transaction, audit); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	if err := transaction.Commit(); err != nil {
		return models.PluginConfigurationPointers{}, err
	}
	return models.PluginConfigurationPointers{
		InstanceID: instanceID, CurrentRevision: pointers.PreviousRevision,
		PreviousRevision: pointers.CurrentRevision,
	}, nil
}

func (store *SQLitePluginConfigurationStore) backfillCurrentConfigurations(ctx context.Context) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	rows, err := transaction.QueryContext(ctx, store.queries.queries["list-instances"])
	if err != nil {
		return err
	}
	type legacyConfiguration struct {
		instanceID string
		revision   int64
		settings   []byte
	}
	configurations := make([]legacyConfiguration, 0)
	for rows.Next() {
		var configuration legacyConfiguration
		if err := rows.Scan(&configuration.instanceID, &configuration.revision, &configuration.settings); err != nil {
			_ = rows.Close()
			return err
		}
		if configuration.revision < 1 || !validPluginConfiguration(configuration.settings) {
			_ = rows.Close()
			return errors.New(store.contract.Diagnostics.InvalidDocument)
		}
		configurations = append(configurations, configuration)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, configuration := range configurations {
		now := time.Now().UTC()
		if _, err := transaction.ExecContext(ctx, store.queries.queries["insert-revision"], configuration.instanceID, configuration.revision, store.contract.SchemaVersion, configurationDigest(configuration.settings), configuration.settings, store.contract.States.Active, store.contract.MigrationActor, now.Format(store.contract.TimestampLayout)); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, store.queries.queries["insert-pointer"], configuration.instanceID, configuration.revision); err != nil {
			return err
		}
	}
	return transaction.Commit()
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
	if scanner.Err() != nil || !storeStatement() || len(queries) != 12 {
		return PluginConfigurationSQL{}, errors.New(invalidContract)
	}
	return PluginConfigurationSQL{queries: queries}, nil
}

func scanCurrentConfiguration(row *sql.Row, timestampLayout string) (models.PluginConfigurationRevision, models.PluginConfigurationPointers, error) {
	var revision models.PluginConfigurationRevision
	var createdAt string
	var pointers models.PluginConfigurationPointers
	var currentRevision, previousRevision, pendingRevision sql.NullInt64
	err := row.Scan(&revision.InstanceID, &revision.Revision, &revision.SchemaVersion, &revision.Digest, &revision.SettingsJSON, &revision.State, &revision.Actor, &createdAt, &currentRevision, &previousRevision, &pendingRevision)
	if err != nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, err
	}
	revision.CreatedAt, err = time.Parse(timestampLayout, createdAt)
	if err != nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationPointers{}, err
	}
	pointers = models.PluginConfigurationPointers{InstanceID: revision.InstanceID, CurrentRevision: nullableInt64(currentRevision), PreviousRevision: nullableInt64(previousRevision), PendingRevision: nullableInt64(pendingRevision)}
	return revision, pointers, nil
}

func scanConfigurationRevision(row *sql.Row, timestampLayout string) (models.PluginConfigurationRevision, error) {
	var revision models.PluginConfigurationRevision
	var createdAt string
	if err := row.Scan(&revision.InstanceID, &revision.Revision, &revision.SchemaVersion, &revision.Digest, &revision.SettingsJSON, &revision.State, &revision.Actor, &createdAt); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	parsed, err := time.Parse(timestampLayout, createdAt)
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	revision.CreatedAt = parsed
	return revision, nil
}

func readConfigurationPointers(ctx context.Context, transaction *sql.Tx, queries PluginConfigurationSQL, instanceID string) (models.PluginConfigurationPointers, error) {
	var pointers models.PluginConfigurationPointers
	var currentRevision, previousRevision, pendingRevision sql.NullInt64
	if err := transaction.QueryRowContext(ctx, queries.queries["select-pointers"], instanceID).Scan(&pointers.InstanceID, &currentRevision, &previousRevision, &pendingRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.PluginConfigurationPointers{}, models.PluginConfigurationNotFound{}
		}
		return models.PluginConfigurationPointers{}, err
	}
	pointers.CurrentRevision = nullableInt64(currentRevision)
	pointers.PreviousRevision = nullableInt64(previousRevision)
	pointers.PendingRevision = nullableInt64(pendingRevision)
	return pointers, nil
}

func setConfigurationRevisionState(ctx context.Context, transaction *sql.Tx, queries PluginConfigurationSQL, instanceID string, revision int64, expectedState, state string) error {
	result, err := transaction.ExecContext(ctx, queries.queries["set-revision-state"], state, instanceID, revision, expectedState)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return models.PluginConfigurationConflict{}
	}
	return nil
}

func updatePluginInstanceSettings(ctx context.Context, transaction *sql.Tx, queries PluginConfigurationSQL, instanceID string, revision int64, settingsJSON []byte, updatedAt time.Time, timestampLayout string) error {
	result, err := transaction.ExecContext(ctx, queries.queries["update-instance-settings"], settingsJSON, revision, updatedAt.Format(timestampLayout), instanceID)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return models.PluginConfigurationNotFound{}
	}
	return nil
}

func updateConfigurationPointers(ctx context.Context, transaction *sql.Tx, queries PluginConfigurationSQL, instanceID string, currentRevision, previousRevision, pendingRevision int64, updatedAt time.Time, timestampLayout string) error {
	result, err := transaction.ExecContext(ctx, queries.queries["set-pointers"], nullableRevision(currentRevision), nullableRevision(previousRevision), nullableRevision(pendingRevision), updatedAt.Format(timestampLayout), instanceID)
	if err != nil {
		return err
	}
	if err := requireOneRow(result); err != nil {
		return models.PluginConfigurationNotFound{}
	}
	return nil
}

func validPluginConfiguration(settings []byte) bool {
	var document map[string]json.RawMessage
	return len(settings) > 0 && json.Unmarshal(settings, &document) == nil && document != nil
}

func validConfigurationAudit(audit models.AuditRecord) bool {
	return audit.Actor != "" && audit.Action != "" && audit.Resource != "" && audit.Result != "" && audit.RequestID != ""
}

func configurationDigest(document []byte) string {
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:])
}

func nullableRevision(revision int64) any {
	if revision == 0 {
		return nil
	}
	return revision
}

func nullableInt64(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
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

func validPluginConfigurationContract(contract pluginConfigurationStoreContract) bool {
	states := []string{contract.States.Active, contract.States.Candidate, contract.States.Superseded, contract.States.Failed}
	for index, state := range states {
		if state == "" || contains(states[:index], state) {
			return false
		}
	}
	return contract.SchemaVersion > 0 && contract.TimestampLayout != "" && contract.MigrationActor != "" &&
		contract.Diagnostics.InvalidContract != "" && contract.Diagnostics.InvalidDocument != "" && contract.Diagnostics.InvalidStore != ""
}
