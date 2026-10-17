package storage

import (
	"context"
	"database/sql"
	"errors"
)

const createServiceSecretReferencesV17SQL = `CREATE TABLE service_secret_references (
    reference TEXT PRIMARY KEY,
    service_id TEXT NOT NULL REFERENCES service_instances(id) ON DELETE RESTRICT,
    purpose TEXT NOT NULL,
    label TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('active','revoked')),
    created_at TEXT NOT NULL
);
CREATE INDEX service_secret_references_service_purpose ON service_secret_references(service_id, purpose, state, created_at, reference);
INSERT INTO schema_migrations(version) VALUES (17);`

func migrateServiceSecretReferences(ctx context.Context, database *sql.DB) error {
	if database == nil {
		return errors.New("service secret reference migration failed")
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("service secret reference migration failed")
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil || version != 16 {
		return errors.New("service secret reference migration failed")
	}
	if _, err := tx.ExecContext(ctx, createServiceSecretReferencesV17SQL); err != nil {
		return errors.New("service secret reference migration failed")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("service secret reference migration failed")
	}
	return nil
}
