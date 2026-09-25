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

type sqliteGroupReleaseStoreContract struct {
	SelectIdempotency        string `yaml:"selectIdempotency"`
	DeleteExpiredIdempotency string `yaml:"deleteExpiredIdempotency"`
	SelectOperation          string `yaml:"selectOperation"`
	SelectCurrent            string `yaml:"selectCurrent"`
	InsertOperation          string `yaml:"insertOperation"`
	InsertIdempotency        string `yaml:"insertIdempotency"`
	InsertJournal            string `yaml:"insertJournal"`
	InsertRevision           string `yaml:"insertRevision"`
	AdvancePointers          string `yaml:"advancePointers"`
	UpdateOperation          string `yaml:"updateOperation"`
	UpdateJournal            string `yaml:"updateJournal"`
	SelectPending            string `yaml:"selectPending"`
	GroupExists              string `yaml:"groupExists"`
	GroupNotFound            string `yaml:"groupNotFound"`
	OperationNotFound        string `yaml:"operationNotFound"`
	RevisionConflict         string `yaml:"revisionConflict"`
	IdempotencyConflict      string `yaml:"idempotencyConflict"`
	InvalidContract          string `yaml:"invalidContract"`
	TimestampLayout          string `yaml:"timestampLayout"`
	PendingState             string `yaml:"pendingState"`
	FailedState              string `yaml:"failedState"`
}

type SQLiteGroupReleaseStore struct {
	database *sql.DB
	contract sqliteGroupReleaseStoreContract
	audit    *SQLiteAuditStore
}

var _ interfaces.GroupReleaseStore = (*SQLiteGroupReleaseStore)(nil)

func NewSQLiteGroupReleaseStore(database *sql.DB) (*SQLiteGroupReleaseStore, error) {
	contents, err := assets.Contract(assets.SQLiteGroupReleaseStore)
	if err != nil {
		return nil, err
	}
	var contract sqliteGroupReleaseStoreContract
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		return nil, err
	}
	if database == nil || contract.SelectIdempotency == "" || contract.DeleteExpiredIdempotency == "" || contract.SelectOperation == "" || contract.SelectCurrent == "" || contract.InsertOperation == "" || contract.InsertIdempotency == "" || contract.InsertJournal == "" || contract.InsertRevision == "" || contract.AdvancePointers == "" || contract.UpdateOperation == "" || contract.UpdateJournal == "" || contract.SelectPending == "" || contract.GroupExists == "" || contract.GroupNotFound == "" || contract.OperationNotFound == "" || contract.RevisionConflict == "" || contract.IdempotencyConflict == "" || contract.InvalidContract == "" || contract.TimestampLayout == "" || contract.PendingState == "" || contract.FailedState == "" {
		return nil, errors.New(contract.InvalidContract)
	}
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	return &SQLiteGroupReleaseStore{database: database, contract: contract, audit: audit}, nil
}

func (store *SQLiteGroupReleaseStore) Reserve(ctx context.Context, reservation models.GroupReleaseReservation) (models.Operation, bool, error) {
	if reservation.GroupID == "" || reservation.Actor == "" || reservation.Scope == "" || reservation.KeyDigest == "" || reservation.RequestDigest == "" || reservation.OperationID == "" || reservation.OperationKind == "" || reservation.OperationState == "" || reservation.RequestID == "" || reservation.CaddyfileDigest == "" || reservation.CaddyfilePath == "" || reservation.CreatedAt.IsZero() || reservation.ExpiresAt.IsZero() {
		return models.Operation{}, false, errors.New(store.contract.InvalidContract)
	}
	now := reservation.CreatedAt.UTC().Format(store.contract.TimestampLayout)
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.Operation{}, false, err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, store.contract.DeleteExpiredIdempotency, reservation.Actor, reservation.Scope, reservation.KeyDigest, now); err != nil {
		return models.Operation{}, false, err
	}
	var requestDigest, operationID string
	err = transaction.QueryRowContext(ctx, store.contract.SelectIdempotency, reservation.Actor, reservation.Scope, reservation.KeyDigest, now).Scan(&requestDigest, &operationID)
	if err == nil {
		if requestDigest != reservation.RequestDigest {
			return models.Operation{}, false, models.IdempotencyConflict{Message: store.contract.IdempotencyConflict}
		}
		operation, readErr := scanOperation(transaction.QueryRowContext(ctx, store.contract.SelectOperation, operationID), store.contract.TimestampLayout)
		if readErr != nil {
			return models.Operation{}, false, readErr
		}
		return operation, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return models.Operation{}, false, err
	}
	var groupExists bool
	if err := transaction.QueryRowContext(ctx, store.contract.GroupExists, reservation.GroupID).Scan(&groupExists); err != nil {
		return models.Operation{}, false, err
	}
	if !groupExists {
		return models.Operation{}, false, models.GroupNotFound{Message: store.contract.GroupNotFound}
	}
	var current sql.NullString
	if err := transaction.QueryRowContext(ctx, store.contract.SelectCurrent, reservation.GroupID).Scan(&current); err != nil {
		return models.Operation{}, false, err
	}
	if !sameOptionalString(reservation.ExpectedCurrentRevision, current) {
		actual := nullableString(current)
		return models.Operation{}, false, models.GroupRevisionConflict{Expected: reservation.ExpectedCurrentRevision, Actual: actual, Message: store.contract.RevisionConflict}
	}
	expiresAt := reservation.ExpiresAt.UTC().Format(store.contract.TimestampLayout)
	if _, err := transaction.ExecContext(ctx, store.contract.InsertOperation,
		reservation.OperationID, reservation.OperationKind, reservation.OperationState, reservation.RequestID,
		reservation.Actor, reservation.GroupID, now, now); err != nil {
		return models.Operation{}, false, err
	}
	if _, err := transaction.ExecContext(ctx, store.contract.InsertIdempotency,
		reservation.Actor, reservation.Scope, reservation.KeyDigest, reservation.RequestDigest,
		reservation.OperationID, now, expiresAt); err != nil {
		return models.Operation{}, false, err
	}
	if _, err := transaction.ExecContext(ctx, store.contract.InsertJournal,
		reservation.OperationID, reservation.GroupID, optionalString(reservation.ExpectedCurrentRevision),
		reservation.RevisionID, reservation.CaddyfileDigest, optionalString(reservation.ArtifactDigest),
		reservation.CaddyfilePath, optionalString(reservation.ArtifactPath), reservation.Actor, reservation.RequestID,
		reservation.OperationState, now, now); err != nil {
		return models.Operation{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return models.Operation{}, false, err
	}
	return models.Operation{
		ID: reservation.OperationID, Kind: reservation.OperationKind, State: reservation.OperationState,
		CreatedAt: reservation.CreatedAt.UTC(), RequestID: reservation.RequestID,
		Actor: reservation.Actor, Resource: reservation.GroupID,
	}, false, nil
}

func (store *SQLiteGroupReleaseStore) Commit(ctx context.Context, commit models.GroupReleaseCommit) error {
	if commit.GroupID == "" || commit.Revision.ID == "" || commit.Revision.GroupID != commit.GroupID || commit.Revision.CaddyfileDigest == "" || commit.Revision.CaddyfilePath == "" || commit.OperationID == "" || commit.OperationState == "" || commit.JournalState == "" || commit.UpdatedAt.IsZero() {
		return errors.New(store.contract.InvalidContract)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if !commit.RevisionAlreadyExists {
		if _, err := transaction.ExecContext(ctx, store.contract.InsertRevision,
			commit.Revision.ID, commit.Revision.GroupID, commit.Revision.CaddyfileDigest,
			optionalString(commit.Revision.ArtifactDigest), commit.Revision.CaddyfilePath,
			optionalString(commit.Revision.ArtifactPath), commit.Revision.Actor); err != nil {
			return err
		}
	}
	result, err := transaction.ExecContext(ctx, store.contract.AdvancePointers, commit.Revision.ID, commit.GroupID, optionalString(commit.ExpectedCurrentRevision))
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return models.GroupRevisionConflict{Expected: commit.ExpectedCurrentRevision, Message: store.contract.RevisionConflict}
	}
	updatedAt := commit.UpdatedAt.UTC().Format(store.contract.TimestampLayout)
	operationResult, err := transaction.ExecContext(ctx, store.contract.UpdateOperation, commit.OperationState, updatedAt, commit.OperationID)
	if err != nil {
		return err
	}
	updatedOperations, err := operationResult.RowsAffected()
	if err != nil {
		return err
	}
	if updatedOperations != 1 {
		return models.OperationNotFound{Message: store.contract.OperationNotFound}
	}
	journalResult, err := transaction.ExecContext(ctx, store.contract.UpdateJournal, commit.JournalState, updatedAt, commit.OperationID)
	if err != nil {
		return err
	}
	updatedJournals, err := journalResult.RowsAffected()
	if err != nil {
		return err
	}
	if updatedJournals != 1 {
		return models.OperationNotFound{Message: store.contract.OperationNotFound}
	}
	if err := store.audit.append(ctx, transaction, commit.Audit); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *SQLiteGroupReleaseStore) Fail(ctx context.Context, operationID, operationState, journalState string, record models.AuditRecord) error {
	if operationID == "" || operationState == "" || journalState == "" {
		return errors.New(store.contract.InvalidContract)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	updatedAt := time.Now().UTC().Format(store.contract.TimestampLayout)
	operationResult, err := transaction.ExecContext(ctx, store.contract.UpdateOperation, operationState, updatedAt, operationID)
	if err != nil {
		return err
	}
	updatedOperations, err := operationResult.RowsAffected()
	if err != nil {
		return err
	}
	if updatedOperations != 1 {
		return models.OperationNotFound{Message: store.contract.OperationNotFound}
	}
	journalResult, err := transaction.ExecContext(ctx, store.contract.UpdateJournal, journalState, updatedAt, operationID)
	if err != nil {
		return err
	}
	updatedJournals, err := journalResult.RowsAffected()
	if err != nil {
		return err
	}
	if updatedJournals != 1 {
		return models.OperationNotFound{Message: store.contract.OperationNotFound}
	}
	if err := store.audit.append(ctx, transaction, record); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *SQLiteGroupReleaseStore) Pending(ctx context.Context) ([]models.GroupReleaseReservation, error) {
	rows, err := store.database.QueryContext(ctx, store.contract.SelectPending, store.contract.PendingState)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reservations := make([]models.GroupReleaseReservation, 0)
	for rows.Next() {
		reservation, err := scanPendingRelease(rows, store.contract.TimestampLayout)
		if err != nil {
			return nil, err
		}
		reservations = append(reservations, reservation)
	}
	return reservations, rows.Err()
}

type operationRow interface {
	Scan(...any) error
}

func scanOperation(row operationRow, layout string) (models.Operation, error) {
	var operation models.Operation
	var createdAt string
	var updatedAt sql.NullString
	if err := row.Scan(&operation.ID, &operation.Kind, &operation.State, &createdAt, &updatedAt, &operation.RequestID, &operation.Actor, &operation.Resource); err != nil {
		return models.Operation{}, err
	}
	var err error
	operation.CreatedAt, err = time.Parse(layout, createdAt)
	if err != nil {
		return models.Operation{}, err
	}
	if updatedAt.Valid {
		updated, parseErr := time.Parse(layout, updatedAt.String)
		if parseErr != nil {
			return models.Operation{}, parseErr
		}
		operation.UpdatedAt = &updated
	}
	return operation, nil
}

func scanPendingRelease(row operationRow, layout string) (models.GroupReleaseReservation, error) {
	var reservation models.GroupReleaseReservation
	var expected, artifactDigest, artifactPath sql.NullString
	var createdAt, expiresAt string
	if err := row.Scan(&reservation.OperationID, &reservation.GroupID, &expected, &reservation.RevisionID,
		&reservation.OperationKind, &reservation.CaddyfileDigest, &artifactDigest, &reservation.CaddyfilePath, &artifactPath,
		&reservation.Actor, &reservation.RequestID, &createdAt, &expiresAt); err != nil {
		return models.GroupReleaseReservation{}, err
	}
	reservation.ExpectedCurrentRevision = nullableString(expected)
	reservation.ArtifactDigest = nullableString(artifactDigest)
	reservation.ArtifactPath = nullableString(artifactPath)
	var err error
	reservation.CreatedAt, err = time.Parse(layout, createdAt)
	if err != nil {
		return models.GroupReleaseReservation{}, err
	}
	reservation.ExpiresAt, err = time.Parse(layout, expiresAt)
	if err != nil {
		return models.GroupReleaseReservation{}, err
	}
	return reservation, nil
}

func sameOptionalString(expected *string, actual sql.NullString) bool {
	if expected == nil {
		return !actual.Valid
	}
	return actual.Valid && *expected == actual.String
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func optionalString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
