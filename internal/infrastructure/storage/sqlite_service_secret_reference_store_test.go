package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func TestSQLiteServiceSecretReferenceStorePersistsAndAuditsOnlyMetadata(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "core.db"), SQLiteOptions{
		Driver: "sqlite", ParentDirectoryMode: 0o700, DatabaseFileMode: 0o600,
		MaxOpenConnections: 1, MaxIdleConnections: 1, SchemaVersion: 17,
		HasMigrationTableQuery: `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')`,
		MigrationVersionQuery:  `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`,
		SchemaVersionError:     "schema version mismatch",
		IntegrityCheckQuery:    "PRAGMA quick_check(1)", ForeignKeyCheckQuery: "PRAGMA foreign_key_check",
		IntegritySuccess: "ok", IntegrityError: "integrity check failed",
		Pragmas: "PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA synchronous = FULL; PRAGMA busy_timeout = 5000;",
	}, []byte(SchemaSQL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := RegisterServiceInstance(ctx, database, "server", []byte(`{}`), "ready", "2026-10-17T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteServiceSecretReferenceStore(database)
	if err != nil {
		t.Fatal(err)
	}
	reference := models.ServiceSecretReference{
		ID: "ssr:v1:0123456789abcdef0123456789abcdef", ServiceID: "server",
		Purpose: "server.http.auth.bearer", Label: "Production token", State: models.ServiceSecretReferenceActive,
	}
	created, err := store.Create(ctx, reference, secretReferenceAudit("service_secret_reference.create"))
	if err != nil {
		t.Fatalf("create reference metadata: %v", err)
	}
	if created.CreatedAt == "" || created.Validate() != nil {
		t.Fatalf("created metadata is incomplete: %#v", created)
	}
	if _, err := store.Create(ctx, reference, secretReferenceAudit("service_secret_reference.create")); err == nil {
		t.Fatal("duplicate reference ID was accepted")
	}
	items, err := store.List(ctx, "server", "server.http.auth.bearer")
	if err != nil || len(items) != 1 || items[0] != created {
		t.Fatalf("list metadata: %#v %v", items, err)
	}
	if _, err := store.List(ctx, "server", "server.tls.certificate"); err != nil {
		t.Fatal(err)
	} else if values, _ := store.List(ctx, "server", "server.tls.certificate"); len(values) != 0 {
		t.Fatalf("purpose filter returned unrelated metadata: %#v", values)
	}
	if err := store.Revoke(ctx, "server", reference.ID, secretReferenceAudit("service_secret_reference.revoke")); err != nil {
		t.Fatalf("revoke reference metadata: %v", err)
	}
	revoked, err := store.Get(ctx, "server", reference.ID)
	if err != nil || revoked.State != models.ServiceSecretReferenceRevoked {
		t.Fatalf("get revoked metadata: %#v %v", revoked, err)
	}
	var count int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action LIKE 'service_secret_reference.%'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("metadata changes and audit records should commit together: count=%d err=%v", count, err)
	}
	var tableColumns string
	if err := database.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='service_secret_references'`).Scan(&tableColumns); err != nil {
		t.Fatalf("read catalog definition: %v", err)
	}
	if containsCatalogColumnName(tableColumns, "value", "path", "handle", "locator") {
		t.Fatalf("catalog schema can store secret material or locators: %s", tableColumns)
	}
	var schemaVersion int
	if err := database.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&schemaVersion); err != nil || schemaVersion != 17 {
		t.Fatalf("SQLite migration version=%d err=%v", schemaVersion, err)
	}
}

func secretReferenceAudit(action string) models.AuditRecord {
	return models.AuditRecord{Actor: "studio-cli", Action: action, Resource: "server", Result: "success", RequestID: "req_secret_catalog"}
}

func containsCatalogColumnName(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(strings.ToLower(value), fragment) {
			return true
		}
	}
	return false
}
