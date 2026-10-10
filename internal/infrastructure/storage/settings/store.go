// Package settings persists Core's own settings independently of plugin storage
// and bootstrap configuration. The caller owns the database and its lifetime.
package settings

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// Error is a safe, typed failure without SQL, document or validator diagnostics.
type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrNotInitialized     Error = "settings not initialized"
	ErrAlreadyInitialized Error = "settings already initialized"
	ErrConflict           Error = "settings revision conflict"
	ErrInvalidDocument    Error = "invalid settings JSON"
	ErrInvalidActor       Error = "invalid settings actor"
	ErrValidation         Error = "settings startup validation failed"
	ErrRevisionNotFound   Error = "settings revision not found"
	ErrStorage            Error = "settings storage failed"
)

// Snapshot owns its document slices. Zero effective revision and nil effective
// document mean no candidate has ever passed explicit startup validation.
type Store struct{ db *sql.DB }

// New borrows db; it neither initializes the schema nor closes the database.
func New(db *sql.DB) *Store { return &Store{db: db} }

// EnsureSchema explicitly initializes only the settings schema, atomically.
// Call after the existing storage migrations; their tables and order are untouched.
func EnsureSchema(db *sql.DB) error {
	if db == nil {
		return ErrStorage
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return safeError(ctx, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, statement := range schema {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return safeError(ctx, err)
		}
	}
	return safeError(ctx, tx.Commit())
}

// Init creates the first desired revision, pending startup validation. Repeated
// calls are rejected, including calls with exactly the same document.
func (s *Store) Init(ctx context.Context, raw []byte, actor string) (models.CoreSettingsSnapshot, error) {
	raw = bytes.Clone(raw)
	if err := validateInput(raw, actor); err != nil {
		return models.CoreSettingsSnapshot{}, err
	}
	return s.write(ctx, func(tx *sql.Tx) (models.CoreSettingsSnapshot, error) {
		_, err := read(ctx, tx)
		if err == nil {
			return models.CoreSettingsSnapshot{}, ErrAlreadyInitialized
		}
		if !errors.Is(err, ErrNotInitialized) {
			return models.CoreSettingsSnapshot{}, err
		}
		if _, err := tx.ExecContext(ctx, insertRevision, int64(1), raw); err != nil {
			return models.CoreSettingsSnapshot{}, err
		}
		if _, err := tx.ExecContext(ctx, insertState); err != nil {
			return models.CoreSettingsSnapshot{}, err
		}
		after, err := read(ctx, tx)
		if err != nil {
			return models.CoreSettingsSnapshot{}, err
		}
		return after, audit(ctx, tx, "init", actor, models.CoreSettingsSnapshot{}, after, nil)
	})
}

func (s *Store) Read(ctx context.Context) (models.CoreSettingsSnapshot, error) {
	if s == nil || s.db == nil {
		return models.CoreSettingsSnapshot{}, ErrStorage
	}
	result, err := read(ctx, s.db)
	return result, safeError(ctx, err)
}

// Update appends exact raw bytes using CAS on the desired revision. The last
// effective revision remains intact until explicit activation succeeds.
func (s *Store) Update(ctx context.Context, expectedRevision int64, raw []byte, actor string) (models.CoreSettingsSnapshot, error) {
	raw = bytes.Clone(raw)
	if err := validateInput(raw, actor); err != nil {
		return models.CoreSettingsSnapshot{}, err
	}
	return s.write(ctx, func(tx *sql.Tx) (models.CoreSettingsSnapshot, error) {
		before, err := read(ctx, tx)
		if err != nil {
			return models.CoreSettingsSnapshot{}, err
		}
		if before.DesiredRevision != expectedRevision {
			return models.CoreSettingsSnapshot{}, ErrConflict
		}
		return appendRevision(ctx, tx, before, raw, "update", actor, nil)
	})
}

// Activate validates the desired bytes before making them effective. The
// validator is mandatory even if desired is already effective (every startup
// must validate). It runs without a database lock and receives a private copy.
// A concurrent pointer change invalidates the result. Validator errors are
// deliberately not returned because they can contain settings and secrets.
func (s *Store) Activate(ctx context.Context, expectedRevision int64, validator func([]byte) error) (models.CoreSettingsSnapshot, error) {
	if validator == nil {
		return models.CoreSettingsSnapshot{}, ErrValidation
	}
	validated, err := s.Read(ctx)
	if err != nil {
		return models.CoreSettingsSnapshot{}, err
	}
	if validated.DesiredRevision != expectedRevision {
		return models.CoreSettingsSnapshot{}, ErrConflict
	}
	if err := validator(bytes.Clone(validated.DesiredDocument)); err != nil {
		if ctx.Err() != nil {
			return models.CoreSettingsSnapshot{}, ctx.Err()
		}
		return models.CoreSettingsSnapshot{}, ErrValidation
	}
	return s.write(ctx, func(tx *sql.Tx) (models.CoreSettingsSnapshot, error) {
		before, err := read(ctx, tx)
		if err != nil {
			return models.CoreSettingsSnapshot{}, err
		}
		if before.DesiredRevision != validated.DesiredRevision || before.EffectiveRevision != validated.EffectiveRevision {
			return models.CoreSettingsSnapshot{}, ErrConflict
		}
		if !before.Pending {
			return before, nil
		}
		if _, err := tx.ExecContext(ctx, updateEffective, expectedRevision, expectedRevision); err != nil {
			return models.CoreSettingsSnapshot{}, err
		}
		after, err := read(ctx, tx)
		if err != nil {
			return models.CoreSettingsSnapshot{}, err
		}
		return after, audit(ctx, tx, "activate", "startup", before, after, nil)
	})
}

// Recover requires the caller to explicitly establish offline operation: Core
// must be stopped and no online settings writer may run. This storage-only
// package cannot detect process lifecycle. Recovery/rollback appends a copy of
// the target revision as a NEW desired revision, preserving all history and the
// last effective pointer. The next startup must Activate it with validation.
func (s *Store) Recover(ctx context.Context, targetRevision int64, actor string) (models.CoreSettingsSnapshot, error) {
	if !validActor(actor) {
		return models.CoreSettingsSnapshot{}, ErrInvalidActor
	}
	if targetRevision < 1 {
		return models.CoreSettingsSnapshot{}, ErrRevisionNotFound
	}
	return s.write(ctx, func(tx *sql.Tx) (models.CoreSettingsSnapshot, error) {
		before, err := read(ctx, tx)
		if err != nil {
			return models.CoreSettingsSnapshot{}, err
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, readRevision, targetRevision).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return models.CoreSettingsSnapshot{}, ErrRevisionNotFound
			}
			return models.CoreSettingsSnapshot{}, err
		}
		if !validJSON(raw) {
			return models.CoreSettingsSnapshot{}, ErrInvalidDocument
		}
		return appendRevision(ctx, tx, before, raw, "recover", actor, targetRevision)
	})
}

func appendRevision(ctx context.Context, tx *sql.Tx, before models.CoreSettingsSnapshot, raw []byte, operation, actor string, source any) (models.CoreSettingsSnapshot, error) {
	if before.DesiredRevision == math.MaxInt64 {
		return models.CoreSettingsSnapshot{}, ErrConflict
	}
	next := before.DesiredRevision + 1
	if _, err := tx.ExecContext(ctx, insertRevision, next, raw); err != nil {
		return models.CoreSettingsSnapshot{}, err
	}
	if _, err := tx.ExecContext(ctx, updateDesired, next, before.DesiredRevision); err != nil {
		return models.CoreSettingsSnapshot{}, err
	}
	after, err := read(ctx, tx)
	if err != nil {
		return models.CoreSettingsSnapshot{}, err
	}
	return after, audit(ctx, tx, operation, actor, before, after, source)
}

func audit(ctx context.Context, tx *sql.Tx, operation, actor string, before, after models.CoreSettingsSnapshot, source any) error {
	_, err := tx.ExecContext(ctx, insertAudit, actor, operation, before.DesiredRevision, before.EffectiveRevision, after.DesiredRevision, after.EffectiveRevision, source)
	return err
}

type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func read(ctx context.Context, q reader) (models.CoreSettingsSnapshot, error) {
	var result models.CoreSettingsSnapshot
	err := q.QueryRowContext(ctx, readState).Scan(&result.DesiredRevision, &result.EffectiveRevision, &result.DesiredDocument, &result.EffectiveDocument)
	if errors.Is(err, sql.ErrNoRows) {
		return models.CoreSettingsSnapshot{}, ErrNotInitialized
	}
	if err != nil {
		return models.CoreSettingsSnapshot{}, err
	}
	result.Pending = result.DesiredRevision != result.EffectiveRevision
	return result, nil
}

func (s *Store) write(ctx context.Context, change func(*sql.Tx) (models.CoreSettingsSnapshot, error)) (models.CoreSettingsSnapshot, error) {
	if s == nil || s.db == nil {
		return models.CoreSettingsSnapshot{}, ErrStorage
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.CoreSettingsSnapshot{}, safeError(ctx, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, lockState); err != nil {
		return models.CoreSettingsSnapshot{}, safeError(ctx, err)
	}
	result, err := change(tx)
	if err != nil {
		return models.CoreSettingsSnapshot{}, safeError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return models.CoreSettingsSnapshot{}, safeError(ctx, err)
	}
	return result, nil
}

func safeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var typed Error
	if errors.As(err, &typed) {
		return typed
	}
	return ErrStorage
}

func validateInput(raw []byte, actor string) error {
	if !validActor(actor) {
		return ErrInvalidActor
	}
	if !validJSON(raw) {
		return ErrInvalidDocument
	}
	return nil
}

// Actors are bounded audit identifiers, not arbitrary messages or credentials.
func validActor(actor string) bool {
	if len(actor) == 0 || len(actor) > 128 {
		return false
	}
	for _, c := range actor {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '.', '-', '_', ':', '@', '/':
		default:
			return false
		}
	}
	return true
}

// Validate syntax and UTF-8, then compare decoded keys at every object nesting
// level. UseNumber avoids float overflow for otherwise valid JSON numbers.
func validJSON(raw []byte) bool {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return uniqueValue(d)
}

func uniqueValue(d *json.Decoder) bool {
	token, err := d.Token()
	if err != nil {
		return false
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return true
	}
	keys := make(map[string]struct{})
	for d.More() {
		if delimiter == '{' {
			token, err := d.Token()
			if err != nil {
				return false
			}
			key, ok := token.(string)
			if !ok {
				return false
			}
			if _, exists := keys[key]; exists {
				return false
			}
			keys[key] = struct{}{}
		}
		if !uniqueValue(d) {
			return false
		}
	}
	_, err = d.Token()
	return err == nil
}
