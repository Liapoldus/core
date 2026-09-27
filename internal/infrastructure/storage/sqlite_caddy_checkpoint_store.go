package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type sqliteCaddyCheckpointContract struct {
	InsertOperation  string `yaml:"insertOperation"`
	InsertCheckpoint string `yaml:"insertCheckpoint"`
	UpdateOperation  string `yaml:"updateOperation"`
	SelectLatest     string `yaml:"selectLatestCheckpoint"`
	TimestampLayout  string `yaml:"timestampLayout"`
	InvalidContract  string `yaml:"invalidContract"`
}

type SQLiteCaddyCheckpointStore struct {
	database *sql.DB
	contract sqliteCaddyCheckpointContract
	audit    *SQLiteAuditStore
}

var _ interfaces.CaddyCheckpointStore = (*SQLiteCaddyCheckpointStore)(nil)

func NewSQLiteCaddyCheckpointStore(database *sql.DB) (*SQLiteCaddyCheckpointStore, error) {
	contents, err := assets.Contract(assets.SQLiteCaddyCheckpoints)
	if err != nil {
		return nil, err
	}
	var contract sqliteCaddyCheckpointContract
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		return nil, err
	}
	if database == nil || contract.InsertOperation == "" || contract.InsertCheckpoint == "" || contract.UpdateOperation == "" || contract.SelectLatest == "" || contract.TimestampLayout == "" || contract.InvalidContract == "" {
		return nil, errors.New(contract.InvalidContract)
	}
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	return &SQLiteCaddyCheckpointStore{database: database, contract: contract, audit: audit}, nil
}

func (store *SQLiteCaddyCheckpointStore) CreateMutation(ctx context.Context, checkpoint models.CaddyCheckpoint, operation models.Operation, record models.AuditRecord) error {
	if store == nil {
		return sql.ErrConnDone
	}
	if checkpoint.ID == "" || checkpoint.RuntimeDigest == "" || checkpoint.SnapshotPath == "" || checkpoint.OperationID == "" || checkpoint.Actor == "" || checkpoint.CreatedAt.IsZero() || operation.ID != checkpoint.OperationID || operation.Kind == "" || operation.State == "" || operation.RequestID == "" || operation.CreatedAt.IsZero() || operation.Actor != checkpoint.Actor || record.Actor == "" || record.Action == "" || record.RequestID == "" {
		return errors.New(store.contract.InvalidContract)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	createdAt := checkpoint.CreatedAt.UTC().Format(store.contract.TimestampLayout)
	updatedAt := operation.CreatedAt.UTC().Format(store.contract.TimestampLayout)
	if _, err := transaction.ExecContext(ctx, store.contract.InsertOperation,
		operation.ID, operation.Kind, operation.State, operation.RequestID,
		operation.Actor, operation.Resource, createdAt, updatedAt); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, store.contract.InsertCheckpoint,
		checkpoint.ID, checkpoint.RuntimeDigest, checkpoint.SnapshotPath,
		checkpoint.OperationID, checkpoint.Actor, createdAt); err != nil {
		return err
	}
	if err := store.audit.append(ctx, transaction, record); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *SQLiteCaddyCheckpointStore) CompleteMutation(ctx context.Context, operationID, state string, record models.AuditRecord) error {
	if store == nil {
		return sql.ErrConnDone
	}
	if operationID == "" || state == "" || record.Actor == "" || record.Action == "" || record.RequestID == "" {
		return errors.New(store.contract.InvalidContract)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	updatedAt := time.Now().UTC().Format(store.contract.TimestampLayout)
	result, err := transaction.ExecContext(ctx, store.contract.UpdateOperation, state, updatedAt, operationID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return errors.New(store.contract.InvalidContract)
	}
	if err := store.audit.append(ctx, transaction, record); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *SQLiteCaddyCheckpointStore) Latest(ctx context.Context) (models.CaddyCheckpoint, bool, error) {
	if store == nil {
		return models.CaddyCheckpoint{}, false, sql.ErrConnDone
	}
	var checkpoint models.CaddyCheckpoint
	var createdAt string
	err := store.database.QueryRowContext(ctx, store.contract.SelectLatest).Scan(
		&checkpoint.ID, &checkpoint.RuntimeDigest, &checkpoint.SnapshotPath,
		&checkpoint.OperationID, &checkpoint.Actor, &createdAt, &checkpoint.OperationState,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return models.CaddyCheckpoint{}, false, nil
	}
	if err != nil {
		return models.CaddyCheckpoint{}, false, err
	}
	checkpoint.CreatedAt, err = time.Parse(store.contract.TimestampLayout, createdAt)
	if err != nil {
		return models.CaddyCheckpoint{}, false, err
	}
	return checkpoint, true, nil
}
