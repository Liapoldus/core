package storage

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func TestSQLiteStagingReaderExposesOnlyPendingCandidateAndSnapshotKeepsItPrivate(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "core.db"), SQLiteOptions{
		Driver: "sqlite", ParentDirectoryMode: 0o700, DatabaseFileMode: 0o600,
		MaxOpenConnections: 1, MaxIdleConnections: 1, SchemaVersion: 17,
		HasMigrationTableQuery: `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')`,
		MigrationVersionQuery:  `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`,
		SchemaVersionError:     "schema version mismatch", IntegrityCheckQuery: "PRAGMA quick_check(1)",
		ForeignKeyCheckQuery: "PRAGMA foreign_key_check", IntegritySuccess: "ok", IntegrityError: "integrity check failed",
		Pragmas: "PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA synchronous = FULL; PRAGMA busy_timeout = 5000;",
	}, []byte(SchemaSQL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := RegisterServiceInstance(ctx, database, "server", []byte(`{}`), "ready", "2026-10-18T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteServiceConfigurationStore(database)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := store.Current(ctx, "server")
	if err != nil {
		t.Fatal(err)
	}
	audit := models.AuditRecord{Actor: "test", Action: "candidate", Resource: "server", Result: "succeeded", RequestID: "request"}
	candidate, err := store.CreateCandidate(ctx, "", "", "", "server", current.Revision, 1, []byte(`{"candidate":true}`), audit)
	if err != nil {
		t.Fatal(err)
	}
	staged, found, err := store.Staged(ctx, "server", candidate.Revision)
	if err != nil || !found || staged.State != store.contract.Slots.Staging || string(staged.SettingsJSON) != `{"candidate":true}` {
		t.Fatalf("pending staging candidate = %+v found=%v err=%v", staged, found, err)
	}
	if _, found, err := store.Staged(ctx, "server", candidate.Revision+1); err != nil || found {
		t.Fatalf("unrelated generation was exposed: found=%v err=%v", found, err)
	}
	selectSlots := store.queries.queries["select-slots"]
	store.queries.queries["select-slots"] = `SELECT slot, generation FROM service_config_generations WHERE instance_id = ? AND slot <> 'staging';`
	if _, found, err := store.Staged(ctx, "server", candidate.Revision); err != nil || found {
		t.Fatalf("staging row without the pending pointer remained grantable: found=%v err=%v", found, err)
	}
	store.queries.queries["select-slots"] = selectSlots
	snapshot, err := NewServiceConfigurationSnapshot(ctx, store, []string{"server"})
	if err != nil {
		t.Fatal(err)
	}
	if _, active, err := snapshot.Retained(ctx, "server", candidate.Revision); err == nil || active {
		t.Fatalf("staging candidate became pullable: active=%v err=%v", active, err)
	}
	if _, err := store.FailCandidate(ctx, "server", candidate.Revision, current.Revision, models.AuditRecord{
		Actor: "test", Action: "failed", Resource: "server", Result: "failed", RequestID: "request-failed",
	}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Staged(ctx, "server", candidate.Revision); err != nil || found {
		t.Fatalf("failed candidate remained grantable: found=%v err=%v", found, err)
	}
}
