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

type sqliteOperationStoreContract struct {
	InsertOperation   string `yaml:"insertOperation"`
	SelectOperation   string `yaml:"selectOperation"`
	TimestampLayout   string `yaml:"timestampLayout"`
	OperationNotFound string `yaml:"operationNotFound"`
	InvalidContract   string `yaml:"invalidContract"`
}

type SQLiteOperationStore struct {
	database *sql.DB
	contract sqliteOperationStoreContract
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
	if database == nil || contract.InsertOperation == "" || contract.SelectOperation == "" || contract.TimestampLayout == "" || contract.OperationNotFound == "" || contract.InvalidContract == "" {
		return nil, errors.New(contract.InvalidContract)
	}
	return &SQLiteOperationStore{database: database, contract: contract}, nil
}

func (store *SQLiteOperationStore) Create(ctx context.Context, operation models.Operation) error {
	if operation.ID == "" || operation.Kind == "" || operation.State == "" || operation.CreatedAt.IsZero() || operation.RequestID == "" {
		return errors.New(store.contract.InvalidContract)
	}
	updatedAt := operation.CreatedAt.UTC().Format(store.contract.TimestampLayout)
	if operation.UpdatedAt != nil {
		updatedAt = operation.UpdatedAt.UTC().Format(store.contract.TimestampLayout)
	}
	_, err := store.database.ExecContext(ctx, store.contract.InsertOperation,
		operation.ID, operation.Kind, operation.State, operation.RequestID,
		operation.Actor, operation.Resource, operation.CreatedAt.UTC().Format(store.contract.TimestampLayout), updatedAt)
	return err
}

func (store *SQLiteOperationStore) Get(ctx context.Context, id string) (models.Operation, error) {
	var operation models.Operation
	var createdAt string
	var updatedAt sql.NullString
	err := store.database.QueryRowContext(ctx, store.contract.SelectOperation, id).Scan(
		&operation.ID, &operation.Kind, &operation.State, &createdAt, &updatedAt,
		&operation.RequestID, &operation.Actor, &operation.Resource)
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
	return operation, nil
}
