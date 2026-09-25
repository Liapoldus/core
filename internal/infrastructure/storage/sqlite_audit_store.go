package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strconv"
	"time"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type sqliteAuditStoreContract struct {
	InsertEvent       string `yaml:"insertAuditEvent"`
	SelectFirstPage   string `yaml:"selectAuditFirstPage"`
	SelectAfterCursor string `yaml:"selectAuditAfterCursor"`
	DeleteExpired     string `yaml:"deleteExpiredAuditEvents"`
	TimestampLayout   string `yaml:"timestampLayout"`
	InvalidContract   string `yaml:"invalidContract"`
	InvalidCursor     string `yaml:"invalidCursor"`
	InvalidLimit      string `yaml:"invalidLimit"`
	AppendFailure     string `yaml:"appendFailure"`
}

type sqliteExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type SQLiteAuditStore struct {
	database *sql.DB
	contract sqliteAuditStoreContract
}

var _ interfaces.AuditStore = (*SQLiteAuditStore)(nil)

func NewSQLiteAuditStore(database *sql.DB) (*SQLiteAuditStore, error) {
	contents, err := assets.Contract(assets.SQLiteAuditStore)
	if err != nil {
		return nil, err
	}
	var contract sqliteAuditStoreContract
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		return nil, err
	}
	if database == nil || contract.InsertEvent == "" || contract.SelectFirstPage == "" || contract.SelectAfterCursor == "" || contract.DeleteExpired == "" || contract.TimestampLayout == "" || contract.InvalidContract == "" || contract.InvalidCursor == "" || contract.InvalidLimit == "" || contract.AppendFailure == "" {
		return nil, errors.New(contract.InvalidContract)
	}
	return &SQLiteAuditStore{database: database, contract: contract}, nil
}

func (store *SQLiteAuditStore) Append(ctx context.Context, record models.AuditRecord) error {
	return store.append(ctx, store.database, record)
}

func (store *SQLiteAuditStore) append(ctx context.Context, executor sqliteExecutor, record models.AuditRecord) error {
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now().UTC()
	}
	_, err := executor.ExecContext(ctx, store.contract.InsertEvent,
		record.Timestamp.UTC().Format(store.contract.TimestampLayout),
		record.Actor, record.Action, record.Resource, record.Result, record.RequestID,
		emptyToNil(record.DigestBefore), emptyToNil(record.DigestAfter))
	if err != nil {
		return models.AuditAppendError{Message: store.contract.AppendFailure}
	}
	return nil
}

func (store *SQLiteAuditStore) List(ctx context.Context, cutoff time.Time, cursor string, limit int) (models.AuditPage, error) {
	if limit < 1 {
		return models.AuditPage{}, models.AuditPageError{Message: store.contract.InvalidLimit}
	}
	var cursorSequence int64
	if cursor != "" {
		sequence, valid := decodeAuditCursor(cursor)
		if !valid {
			return models.AuditPage{}, models.AuditPageError{Message: store.contract.InvalidCursor}
		}
		cursorSequence = sequence
	}
	cutoffValue := cutoff.UTC().Format(store.contract.TimestampLayout)
	if _, err := store.database.ExecContext(ctx, store.contract.DeleteExpired, cutoffValue); err != nil {
		return models.AuditPage{}, err
	}
	query := store.contract.SelectFirstPage
	arguments := []any{cutoffValue, limit + 1}
	if cursor != "" {
		query = store.contract.SelectAfterCursor
		arguments = []any{cutoffValue, cursorSequence, limit + 1}
	}
	rows, err := store.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return models.AuditPage{}, err
	}
	defer rows.Close()
	items := make([]models.AuditRecord, 0, limit+1)
	sequences := make([]int64, 0, limit+1)
	for rows.Next() {
		var sequence int64
		var timestamp string
		var record models.AuditRecord
		var beforeDigest, afterDigest sql.NullString
		if err := rows.Scan(&sequence, &timestamp, &record.Actor, &record.Action, &record.Resource, &record.Result, &record.RequestID, &beforeDigest, &afterDigest); err != nil {
			return models.AuditPage{}, err
		}
		record.Timestamp, err = time.Parse(store.contract.TimestampLayout, timestamp)
		if err != nil {
			return models.AuditPage{}, err
		}
		if beforeDigest.Valid {
			record.DigestBefore = beforeDigest.String
		}
		if afterDigest.Valid {
			record.DigestAfter = afterDigest.String
		}
		items = append(items, record)
		sequences = append(sequences, sequence)
	}
	if err := rows.Err(); err != nil {
		return models.AuditPage{}, err
	}
	page := models.AuditPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		next := encodeAuditCursor(sequences[limit-1])
		page.NextCursor = &next
	}
	return page, nil
}

func encodeAuditCursor(sequence int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(sequence, 10)))
}

func decodeAuditCursor(cursor string) (int64, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, false
	}
	sequence, err := strconv.ParseInt(string(decoded), 10, 64)
	return sequence, err == nil && sequence > 0
}

func emptyToNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
