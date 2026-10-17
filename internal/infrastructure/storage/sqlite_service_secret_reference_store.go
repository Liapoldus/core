package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type sqliteServiceSecretReferenceStoreContract struct {
	Insert          string
	SelectOne       string
	SelectPage      string
	Revoke          string
	TimestampLayout string
	Diagnostics     struct {
		InvalidContract string
		InvalidStore    string
	}
}

// SQLiteServiceSecretReferenceStore persists opaque reference metadata only.
type SQLiteServiceSecretReferenceStore struct {
	database *sql.DB
	contract sqliteServiceSecretReferenceStoreContract
	audit    *SQLiteAuditStore
}

var _ interfaces.ServiceSecretReferenceStore = (*SQLiteServiceSecretReferenceStore)(nil)

func NewSQLiteServiceSecretReferenceStore(database *sql.DB) (*SQLiteServiceSecretReferenceStore, error) {
	contract := serviceSecretReferenceDefinitions()
	if database == nil || contract.Insert == "" || contract.SelectOne == "" || contract.SelectPage == "" ||
		contract.Revoke == "" || contract.TimestampLayout == "" || contract.Diagnostics.InvalidContract == "" || contract.Diagnostics.InvalidStore == "" {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	return &SQLiteServiceSecretReferenceStore{database: database, contract: contract, audit: audit}, nil
}

func (store *SQLiteServiceSecretReferenceStore) Create(
	ctx context.Context,
	reference models.ServiceSecretReference,
	audit models.AuditRecord,
) (models.ServiceSecretReference, error) {
	if store == nil || store.database == nil {
		return models.ServiceSecretReference{}, sql.ErrConnDone
	}
	if reference.State != models.ServiceSecretReferenceActive {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceInvalid{}
	}
	reference.CreatedAt = time.Now().UTC().Format(store.contract.TimestampLayout)
	if reference.Validate() != nil {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceInvalid{}
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.ServiceSecretReference{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, store.contract.Insert, reference.ID, reference.ServiceID, reference.Purpose, reference.Label, reference.State, reference.CreatedAt); err != nil {
		if isUniqueConstraint(err) {
			return models.ServiceSecretReference{}, models.ServiceSecretReferenceConflict{}
		}
		return models.ServiceSecretReference{}, err
	}
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return models.ServiceSecretReference{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.ServiceSecretReference{}, err
	}
	return reference, nil
}

func (store *SQLiteServiceSecretReferenceStore) List(ctx context.Context, serviceID, purpose string) ([]models.ServiceSecretReference, error) {
	if store == nil || store.database == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := store.database.QueryContext(ctx, store.contract.SelectPage, serviceID, purpose, purpose)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]models.ServiceSecretReference, 0)
	for rows.Next() {
		item, err := scanServiceSecretReference(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (store *SQLiteServiceSecretReferenceStore) Get(ctx context.Context, serviceID, referenceID string) (models.ServiceSecretReference, error) {
	if store == nil || store.database == nil {
		return models.ServiceSecretReference{}, sql.ErrConnDone
	}
	item, err := scanServiceSecretReference(store.database.QueryRowContext(ctx, store.contract.SelectOne, serviceID, referenceID))
	if errors.Is(err, sql.ErrNoRows) {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceNotFound{}
	}
	return item, err
}

func (store *SQLiteServiceSecretReferenceStore) Revoke(
	ctx context.Context,
	serviceID, referenceID string,
	audit models.AuditRecord,
) error {
	if store == nil || store.database == nil {
		return sql.ErrConnDone
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, store.contract.Revoke, serviceID, referenceID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		item, err := scanServiceSecretReference(tx.QueryRowContext(ctx, store.contract.SelectOne, serviceID, referenceID))
		if errors.Is(err, sql.ErrNoRows) {
			return models.ServiceSecretReferenceNotFound{}
		}
		if err != nil {
			return err
		}
		if item.State == models.ServiceSecretReferenceRevoked {
			return tx.Commit()
		}
		return errors.New(store.contract.Diagnostics.InvalidStore)
	}
	if changed != 1 {
		return errors.New(store.contract.Diagnostics.InvalidStore)
	}
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

type serviceSecretReferenceScanner interface {
	Scan(...any) error
}

func scanServiceSecretReference(scanner serviceSecretReferenceScanner) (models.ServiceSecretReference, error) {
	var item models.ServiceSecretReference
	err := scanner.Scan(&item.ID, &item.ServiceID, &item.Purpose, &item.Label, &item.State, &item.CreatedAt)
	if err != nil {
		return models.ServiceSecretReference{}, err
	}
	if item.Validate() != nil {
		return models.ServiceSecretReference{}, models.ServiceSecretReferenceInvalid{}
	}
	return item, nil
}
