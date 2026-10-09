package settings

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	_ "modernc.org/sqlite"
)

func openStore(t *testing.T, path string) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func TestRevisions(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.db")
	s, db := openStore(t, path)
	if _, err := s.Read(ctx); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("fresh read: %v", err)
	}
	raw := []byte(" {\n  \"n\": 1e2, \"text\": \"\\u0061\"\n } ")
	first, err := s.Init(ctx, raw, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if first.DesiredRevision != 1 || first.EffectiveRevision != 0 || !first.Pending || !bytes.Equal(first.DesiredDocument, raw) || first.EffectiveDocument != nil {
		t.Fatalf("init: %+v", first)
	}
	if _, err := s.Init(ctx, []byte(`{}`), "operator"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("repeat: %v", err)
	}
	if _, err := s.Update(ctx, 0, []byte(`{}`), "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("CAS: %v", err)
	}
	active, err := s.Activate(ctx, 1, func(got []byte) error {
		if !bytes.Equal(got, raw) {
			t.Fatal("validator did not get exact bytes")
		}
		got[0] = 'x' // The validator cannot mutate the stored or returned document.
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if active.Pending || active.EffectiveRevision != 1 || !bytes.Equal(active.EffectiveDocument, raw) {
		t.Fatalf("active: %+v", active)
	}
	next, err := s.Update(ctx, 1, []byte(`{"invalid_at_startup":true}`), "operator")
	if err != nil {
		t.Fatal(err)
	}
	if next.DesiredRevision != 2 || next.EffectiveRevision != 1 || !next.Pending {
		t.Fatalf("pending: %+v", next)
	}
	// A new connection represents an offline restart. Failed startup validation
	// must preserve both the candidate and the last successfully effective bytes.
	restarted, _ := openStore(t, path)
	if _, err := restarted.Activate(ctx, 2, func([]byte) error { return errors.New("secret validator detail") }); !errors.Is(err, ErrValidation) || err.Error() == "secret validator detail" {
		t.Fatalf("invalid restart: %v", err)
	}
	unchanged, err := restarted.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.DesiredRevision != 2 || unchanged.EffectiveRevision != 1 || !unchanged.Pending {
		t.Fatalf("failed activation changed state: %+v", unchanged)
	}
	recovered, err := restarted.Recover(ctx, 1, "offline-operator")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.DesiredRevision != 3 || recovered.EffectiveRevision != 1 || !recovered.Pending || !bytes.Equal(recovered.DesiredDocument, raw) {
		t.Fatalf("recovery: %+v", recovered)
	}
	rolledBack, err := restarted.Activate(ctx, 3, func([]byte) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Pending || rolledBack.EffectiveRevision != 3 {
		t.Fatalf("rollback: %+v", rolledBack)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM core_settings_revisions`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("history %d: %v", count, err)
	}
	var original []byte
	if err := db.QueryRow(`SELECT document FROM core_settings_revisions WHERE revision=2`).Scan(&original); err != nil || !bytes.Equal(original, []byte(`{"invalid_at_startup":true}`)) {
		t.Fatalf("history overwritten: %q, %v", original, err)
	}
	rows, err := db.Query(`SELECT actor, operation, desired_revision, effective_revision FROM core_settings_audit ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []string{"init", "activate", "update", "recover", "activate"}
	i := 0
	for rows.Next() {
		var actor, operation string
		var desired, effective int64
		if err := rows.Scan(&actor, &operation, &desired, &effective); err != nil {
			t.Fatal(err)
		}
		if i >= len(want) || operation != want[i] || actor == "" || desired < 1 || effective < 0 {
			t.Fatalf("audit row %d: %s %s %d %d", i, actor, operation, desired, effective)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != len(want) {
		t.Fatalf("audit rows: %d", i)
	}
}

func TestInvalidAndAtomicFailures(t *testing.T) {
	ctx := context.Background()
	s, db := openStore(t, filepath.Join(t.TempDir(), "settings.db"))
	for _, raw := range [][]byte{nil, []byte(" "), []byte(`{`), []byte(`{"a":1,"\u0061":2}`), []byte(`{"a":[{"b":1,"b":2}]}`), {0xff}} {
		if _, err := s.Init(ctx, raw, "operator"); !errors.Is(err, ErrInvalidDocument) {
			t.Fatalf("accepted %q: %v", raw, err)
		}
	}
	if _, err := s.Init(ctx, []byte(`{}`), "bad\nactor"); !errors.Is(err, ErrInvalidActor) {
		t.Fatalf("unsafe actor: %v", err)
	}
	if _, err := s.Init(ctx, []byte(`{}`), "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Activate(ctx, 1, nil); !errors.Is(err, ErrValidation) {
		t.Fatalf("nil validator: %v", err)
	}
	if _, err := s.Recover(ctx, 99, "operator"); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("missing target: %v", err)
	}
	// Force a failure after the revision and pointer writes, at the audit insert.
	if _, err := db.Exec(`CREATE TRIGGER reject_audit BEFORE INSERT ON core_settings_audit BEGIN SELECT RAISE(ABORT, 'secret SQL diagnostic'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, 1, []byte(`{"new":true}`), "operator"); !errors.Is(err, ErrStorage) {
		t.Fatalf("audit failure: %v", err)
	}
	got, err := s.Read(ctx)
	if err != nil || got.DesiredRevision != 1 {
		t.Fatalf("transaction rollback: %+v %v", got, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM core_settings_revisions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("orphan revision: %d %v", count, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Read(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("context: %v", err)
	}
}

func TestSchemaIsolationAndImmutableHistory(t *testing.T) {
	ctx := context.Background()
	s, db := openStore(t, filepath.Join(t.TempDir(), "settings.db"))
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY); INSERT INTO schema_migrations VALUES (17)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Init(ctx, []byte(`{"x":1}`), "operator"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow(`SELECT version FROM schema_migrations`).Scan(&version); err != nil || version != 17 {
		t.Fatalf("existing migration history changed: %d %v", version, err)
	}
	for _, query := range []string{
		`UPDATE core_settings_revisions SET document = '{}'`,
		`DELETE FROM core_settings_revisions`,
		`UPDATE core_settings_audit SET actor = 'other'`,
		`DELETE FROM core_settings_audit`,
	} {
		if _, err := db.Exec(query); err == nil {
			t.Fatalf("mutable history: %s", query)
		}
	}
	if _, err := s.Update(ctx, 1, []byte(`{"duplicate":1,"duplicate":2}`), "operator"); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("invalid update: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Update(canceled, 1, []byte(`{}`), "operator"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled update: %v", err)
	}
	if _, err := s.Activate(ctx, 1, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := s.Activate(ctx, 1, func([]byte) error { called = true; return errors.New("startup rejection") }); !errors.Is(err, ErrValidation) || !called {
		t.Fatalf("effective revision skipped startup validation: %v", err)
	}
	got, err := s.Read(ctx)
	if err != nil || got.DesiredRevision != 1 || got.EffectiveRevision != 1 || got.Pending {
		t.Fatalf("snapshot: %+v %v", got, err)
	}
}

func TestSchemaTransactionRollback(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	// A BEFORE trigger cannot be created on a view. Earlier table creation
	// must roll back when this later schema statement fails.
	if _, err := db.Exec(`CREATE VIEW core_settings_audit AS SELECT 1 AS sequence`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(db); !errors.Is(err, ErrStorage) {
		t.Fatalf("schema failure: %v", err)
	}
	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='core_settings_revisions'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("partial schema survived: %d %v", tables, err)
	}
}

func TestConcurrentCASAndActivation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.db")
	s, _ := openStore(t, path)
	other, _ := openStore(t, path)
	if _, err := s.Init(ctx, []byte(`{}`), "operator"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, store := range []*Store{s, other} {
		wg.Go(func() { _, err := store.Update(ctx, 1, []byte(`{"next":true}`), "operator"); results <- err })
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS winners %d conflicts %d", successes, conflicts)
	}
	if _, err := s.Activate(ctx, 2, func([]byte) error {
		_, err := other.Update(ctx, 2, []byte(`{"newer":true}`), "operator")
		return err
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale validated candidate activated: %v", err)
	}
	got, err := s.Read(ctx)
	if err != nil || got.DesiredRevision != 3 || got.EffectiveRevision != 0 {
		t.Fatalf("stale activation: %+v %v", got, err)
	}
}
