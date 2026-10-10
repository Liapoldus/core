package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type sqliteAccessContract struct {
	BeginImmediate        string
	Commit                string
	Rollback              string
	SelectActiveKey       string
	InsertKey             string
	SelectActiveVerifiers string
	SelectKeyMetadata     string
	BootstrapName         string
	InvalidContract       string
	ActiveKeyConflict     string
}

type SQLiteServiceKeyStore struct {
	database *sql.DB
	contract sqliteAccessContract
	audit    *SQLiteAuditStore
}

var _ interfaces.ServiceKeyStore = (*SQLiteServiceKeyStore)(nil)

func NewSQLiteServiceKeyStore(database *sql.DB) (*SQLiteServiceKeyStore, error) {
	contract := accessDefinitions()
	if database == nil || contract.BeginImmediate == "" || contract.Commit == "" || contract.Rollback == "" || contract.SelectActiveKey == "" || contract.InsertKey == "" || contract.SelectActiveVerifiers == "" || contract.SelectKeyMetadata == "" || contract.BootstrapName == "" || contract.InvalidContract == "" || contract.ActiveKeyConflict == "" {
		return nil, errors.New(contract.InvalidContract)
	}
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	return &SQLiteServiceKeyStore{database: database, contract: contract, audit: audit}, nil
}

func (store *SQLiteServiceKeyStore) Create(ctx context.Context, key models.ServiceKey, record models.AuditRecord) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, store.contract.InsertKey, key.ID, key.Name, key.Verifier, key.Role); err != nil {
		return err
	}
	if err := store.audit.append(ctx, transaction, record); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *SQLiteServiceKeyStore) Bootstrap(ctx context.Context, id string, verifier []byte, role string) error {
	connection, err := store.database.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, store.contract.BeginImmediate); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), store.contract.Rollback)
		}
	}()
	var active bool
	if err := connection.QueryRowContext(ctx, store.contract.SelectActiveKey).Scan(&active); err != nil {
		return err
	}
	if active {
		return models.ActiveServiceKeyExists{Message: store.contract.ActiveKeyConflict}
	}
	if _, err := connection.ExecContext(ctx, store.contract.InsertKey, id, store.contract.BootstrapName, verifier, role); err != nil {
		return err
	}
	if _, err := connection.ExecContext(ctx, store.contract.Commit); err != nil {
		return err
	}
	committed = true
	return nil
}

func (store *SQLiteServiceKeyStore) ActiveVerifiers(ctx context.Context) ([]models.ServiceKeyVerifier, error) {
	rows, err := store.database.QueryContext(ctx, store.contract.SelectActiveVerifiers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	verifiers := make([]models.ServiceKeyVerifier, 0)
	for rows.Next() {
		var id string
		var verifier []byte
		if err := rows.Scan(&id, &verifier); err != nil {
			return nil, err
		}
		verifiers = append(verifiers, models.ServiceKeyVerifier{ID: id, Verifier: verifier})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return verifiers, nil
}

func (store *SQLiteServiceKeyStore) Metadata(ctx context.Context) ([]models.ServiceKeyMetadata, error) {
	rows, err := store.database.QueryContext(ctx, store.contract.SelectKeyMetadata)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	metadata := make([]models.ServiceKeyMetadata, 0)
	for rows.Next() {
		var item models.ServiceKeyMetadata
		var expiresAt, revokedAt sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.Role, &item.CreatedAt, &expiresAt, &revokedAt); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			item.ExpiresAt = &expiresAt.String
		}
		if revokedAt.Valid {
			item.RevokedAt = &revokedAt.String
		}
		metadata = append(metadata, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return metadata, nil
}
