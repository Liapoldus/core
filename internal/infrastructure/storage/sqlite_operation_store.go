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
	"strconv"
	"strings"
	"time"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

//go:embed sql/operation.sql
var operationSQL embed.FS

type sqliteOperationStoreContract struct {
	TimestampLayout      string `yaml:"timestampLayout"`
	ErrorCodeField       string `yaml:"errorCodeField"`
	IdempotencyExpiresAt string `yaml:"idempotencyExpiresAt"`
	OperationNotFound    string `yaml:"operationNotFound"`
	InvalidContract      string `yaml:"invalidContract"`
	InvalidReservation   string `yaml:"invalidReservation"`
	TransitionConflict   string `yaml:"transitionConflict"`
	IdempotencyConflict  string `yaml:"idempotencyConflict"`
}

type SQLiteOperationStore struct {
	database         *sql.DB
	contract         sqliteOperationStoreContract
	queries          map[string]string
	idempotencyUntil time.Time
}

type sqliteOperationQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var _ interfaces.OperationStore = (*SQLiteOperationStore)(nil)

func NewSQLiteOperationStore(database *sql.DB) (*SQLiteOperationStore, error) {
	contents, err := assets.Contract(assets.SQLiteOperationStore)
	if err != nil {
		return nil, err
	}
	var contract sqliteOperationStoreContract
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		return nil, err
	}
	expiresAt, expiresErr := time.Parse(contract.TimestampLayout, contract.IdempotencyExpiresAt)
	queries, queryErr := loadOperationSQL(contract.InvalidContract)
	if database == nil || contract.TimestampLayout == "" || contract.ErrorCodeField == "" || expiresErr != nil ||
		contract.OperationNotFound == "" || contract.InvalidContract == "" || contract.InvalidReservation == "" ||
		contract.TransitionConflict == "" || contract.IdempotencyConflict == "" || queryErr != nil {
		return nil, errors.New(contract.InvalidContract)
	}
	return &SQLiteOperationStore{database: database, contract: contract, queries: queries, idempotencyUntil: expiresAt}, nil
}

func (store *SQLiteOperationStore) Create(ctx context.Context, operation models.Operation) error {
	if !validOperation(operation) {
		return errors.New(store.contract.InvalidReservation)
	}
	_, err := store.database.ExecContext(ctx, store.queries["insert-operation"], operationValues(operation, store.contract.TimestampLayout)...)
	return err
}

func (store *SQLiteOperationStore) Reserve(ctx context.Context, reservation models.OperationReservation) (models.Operation, bool, error) {
	operation := reservation.Operation
	if store == nil || store.database == nil || !validOperation(operation) || reservation.Scope == "" || reservation.Key == "" || reservation.RequestDigest == "" {
		return models.Operation{}, false, errors.New(store.contract.InvalidReservation)
	}
	if reservation.Payload != nil && (!reservation.Payload.Valid() || reservation.Payload.Digest != reservation.RequestDigest || reservation.Payload.Resource != operation.Resource) {
		return models.Operation{}, false, errors.New(store.contract.InvalidReservation)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.Operation{}, false, err
	}
	defer transaction.Rollback()
	keyDigest := idempotencyKeyDigest(reservation.Key)
	now := time.Now().UTC()
	nowText := now.Format(store.contract.TimestampLayout)
	if _, err := transaction.ExecContext(ctx, store.queries["delete-expired-idempotency"], operation.Actor, reservation.Scope, keyDigest, nowText); err != nil {
		return models.Operation{}, false, err
	}
	existing, found, err := findReservedOperation(ctx, transaction, store, operation.Actor, reservation.Scope, keyDigest, reservation.RequestDigest, nowText)
	if err != nil {
		return models.Operation{}, false, err
	}
	if found {
		return existing, false, nil
	}
	if _, err := transaction.ExecContext(ctx, store.queries["insert-operation"], operationValues(operation, store.contract.TimestampLayout)...); err != nil {
		return models.Operation{}, false, err
	}
	if _, err := transaction.ExecContext(ctx, store.queries["insert-idempotency"],
		operation.Actor, reservation.Scope, keyDigest, reservation.RequestDigest, operation.ID,
		nowText, store.idempotencyUntil.UTC().Format(store.contract.TimestampLayout)); err != nil {
		return models.Operation{}, false, err
	}
	if reservation.Payload != nil {
		payload := reservation.Payload
		if _, err := transaction.ExecContext(ctx, store.queries["insert-operation-payload"], operation.ID,
			payload.Version, payload.Resource, payload.ExpectedRevision, payload.SchemaVersion, payload.Digest); err != nil {
			return models.Operation{}, false, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return models.Operation{}, false, err
	}
	return operation, true, nil
}

func (store *SQLiteOperationStore) FindByIdempotency(ctx context.Context, actor, scope, key, requestDigest string) (models.Operation, bool, error) {
	if store == nil || store.database == nil || actor == "" || scope == "" || key == "" || requestDigest == "" {
		return models.Operation{}, false, errors.New(store.contract.InvalidReservation)
	}
	operation, found, err := findReservedOperation(ctx, store.database, store, actor, scope, idempotencyKeyDigest(key), requestDigest, time.Now().UTC().Format(store.contract.TimestampLayout))
	return operation, found, err
}

func (store *SQLiteOperationStore) Transition(ctx context.Context, id, fromState, toState, errorCode string) error {
	if store == nil || store.database == nil || id == "" || fromState == "" || toState == "" {
		return errors.New(store.contract.InvalidReservation)
	}
	var problemJSON []byte
	if errorCode != "" {
		encoded, err := json.Marshal(map[string]string{store.contract.ErrorCodeField: errorCode})
		if err != nil {
			return err
		}
		problemJSON = encoded
	}
	result, err := store.database.ExecContext(ctx, store.queries["transition-operation"],
		toState, time.Now().UTC().Format(store.contract.TimestampLayout), problemJSON, id, fromState)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 1 {
		return nil
	}
	operation, err := store.Get(ctx, id)
	if err == nil && operation.State == toState && operation.ErrorCode == errorCode {
		return nil
	}
	return models.OperationTransitionConflict{Message: store.contract.TransitionConflict}
}

func (store *SQLiteOperationStore) ListRecoverable(ctx context.Context, kind, pendingState, runningState string) ([]models.Operation, error) {
	if store == nil || store.database == nil || kind == "" || pendingState == "" || runningState == "" {
		return nil, errors.New(store.contract.InvalidReservation)
	}
	rows, err := store.database.QueryContext(ctx, store.queries["list-recoverable"], kind, pendingState, runningState)
	if err != nil {
		return nil, err
	}
	type recoverableOperation struct {
		id                string
		candidateRevision int64
	}
	recoverable := make([]recoverableOperation, 0)
	for rows.Next() {
		var id string
		var resultJSON []byte
		if err := rows.Scan(&id, &resultJSON); err != nil {
			_ = rows.Close()
			return nil, err
		}
		revision := int64(0)
		if len(resultJSON) > 0 {
			parsed, parseErr := strconv.ParseInt(string(resultJSON), 10, 64)
			if parseErr != nil || parsed < 1 {
				_ = rows.Close()
				return nil, errors.New(store.contract.InvalidContract)
			}
			revision = parsed
		}
		recoverable = append(recoverable, recoverableOperation{id: id, candidateRevision: revision})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	operations := make([]models.Operation, 0, len(recoverable))
	for _, record := range recoverable {
		operation, err := store.Get(ctx, record.id)
		if err != nil {
			return nil, err
		}
		operation.CandidateRevision = record.candidateRevision
		operations = append(operations, operation)
	}
	return operations, nil
}

func (store *SQLiteOperationStore) Get(ctx context.Context, id string) (models.Operation, error) {
	if store == nil || store.database == nil || id == "" {
		return models.Operation{}, errors.New(store.contract.InvalidReservation)
	}
	return readOperation(ctx, store.database, store, id)
}

func (store *SQLiteOperationStore) Payload(ctx context.Context, id string) (models.OperationPayload, bool, error) {
	if store == nil || store.database == nil || id == "" {
		return models.OperationPayload{}, false, errors.New(store.contract.InvalidReservation)
	}
	var payload models.OperationPayload
	err := store.database.QueryRowContext(ctx, store.queries["select-operation-payload"], id).Scan(
		&payload.Version, &payload.Resource, &payload.ExpectedRevision, &payload.SchemaVersion, &payload.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return models.OperationPayload{}, false, nil
	}
	if err != nil {
		return models.OperationPayload{}, false, err
	}
	return payload, true, nil
}

func readOperation(ctx context.Context, queryer sqliteOperationQueryer, store *SQLiteOperationStore, id string) (models.Operation, error) {
	var operation models.Operation
	var createdAt string
	var updatedAt sql.NullString
	var problemJSON []byte
	err := queryer.QueryRowContext(ctx, store.queries["select-operation"], id).Scan(
		&operation.ID, &operation.Kind, &operation.State, &createdAt, &updatedAt,
		&operation.RequestID, &operation.Actor, &operation.Resource, &problemJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Operation{}, models.OperationNotFound{Message: store.contract.OperationNotFound}
	}
	if err != nil {
		return models.Operation{}, err
	}
	operation.CreatedAt, err = time.Parse(store.contract.TimestampLayout, createdAt)
	if err != nil {
		return models.Operation{}, err
	}
	if updatedAt.Valid {
		parsed, parseErr := time.Parse(store.contract.TimestampLayout, updatedAt.String)
		if parseErr != nil {
			return models.Operation{}, parseErr
		}
		operation.UpdatedAt = &parsed
	}
	if len(problemJSON) > 0 {
		var problem map[string]string
		if err := json.Unmarshal(problemJSON, &problem); err != nil {
			return models.Operation{}, err
		}
		operation.ErrorCode = problem[store.contract.ErrorCodeField]
	}
	return operation, nil
}

func findReservedOperation(ctx context.Context, queryer interface {
	sqliteOperationQueryer
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, store *SQLiteOperationStore, actor, scope, keyDigest, requestDigest, now string) (models.Operation, bool, error) {
	var storedDigest, operationID string
	err := queryer.QueryRowContext(ctx, store.queries["select-idempotency"], actor, scope, keyDigest, now).Scan(&storedDigest, &operationID)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Operation{}, false, nil
	}
	if err != nil {
		return models.Operation{}, false, err
	}
	if storedDigest != requestDigest {
		return models.Operation{}, false, models.IdempotencyConflict{Message: store.contract.IdempotencyConflict}
	}
	operation, err := readOperation(ctx, queryer, store, operationID)
	return operation, err == nil, err
}

func loadOperationSQL(invalidContract string) (map[string]string, error) {
	contents, err := operationSQL.ReadFile("sql/operation.sql")
	if err != nil {
		return nil, errors.New(invalidContract)
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
				return nil, errors.New(invalidContract)
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
	if scanner.Err() != nil || !storeStatement() || len(queries) != 9 {
		return nil, errors.New(invalidContract)
	}
	return queries, nil
}

func validOperation(operation models.Operation) bool {
	return operation.ID != "" && operation.Kind != "" && operation.State != "" && !operation.CreatedAt.IsZero() &&
		operation.RequestID != "" && operation.Actor != "" && operation.Resource != ""
}

func operationValues(operation models.Operation, timestampLayout string) []any {
	updatedAt := operation.CreatedAt.UTC().Format(timestampLayout)
	if operation.UpdatedAt != nil {
		updatedAt = operation.UpdatedAt.UTC().Format(timestampLayout)
	}
	return []any{operation.ID, operation.Kind, operation.State, operation.RequestID, operation.Actor, operation.Resource,
		operation.CreatedAt.UTC().Format(timestampLayout), updatedAt}
}

func idempotencyKeyDigest(key string) string {
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}
