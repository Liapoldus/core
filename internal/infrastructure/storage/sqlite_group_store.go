package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type sqliteGroupStoreContract struct {
	InsertApplicationGroup     string `yaml:"insertApplicationGroup"`
	InsertGroupPointer         string `yaml:"insertGroupPointer"`
	SelectGroup                string `yaml:"selectGroup"`
	SelectGroups               string `yaml:"selectGroups"`
	SelectGroupKind            string `yaml:"selectGroupKind"`
	ArchiveApplicationGroup    string `yaml:"archiveApplicationGroup"`
	InsertRevision             string `yaml:"insertRevision"`
	SelectRevision             string `yaml:"selectRevision"`
	SelectRevisionsFirstPage   string `yaml:"selectRevisionsFirstPage"`
	SelectRevisionsAfterCursor string `yaml:"selectRevisionsAfterCursor"`
	SelectRevisionExists       string `yaml:"selectRevisionExists"`
	SelectGroupExists          string `yaml:"selectGroupExists"`
	SelectPointers             string `yaml:"selectPointers"`
	AdvancePointers            string `yaml:"advancePointers"`
	GroupNotFound              string `yaml:"groupNotFound"`
	GroupAlreadyExists         string `yaml:"groupAlreadyExists"`
	RevisionNotFound           string `yaml:"revisionNotFound"`
	RevisionConflict           string `yaml:"revisionConflict"`
	InvalidContract            string `yaml:"invalidContract"`
	SystemGroupArchiveRejected string `yaml:"systemGroupArchiveRejected"`
	InvalidRevisionCursor      string `yaml:"invalidRevisionCursor"`
	InvalidRevisionLimit       string `yaml:"invalidRevisionLimit"`
	CursorSeparator            string `yaml:"cursorSeparator"`
	DefaultRevisionLimit       int    `yaml:"defaultRevisionLimit"`
	MaximumRevisionLimit       int    `yaml:"maximumRevisionLimit"`
	ApplicationGroupKind       string `yaml:"applicationGroupKind"`
}

type SQLiteGroupStore struct {
	database *sql.DB
	queries  sqliteGroupStoreContract
}

var _ interfaces.GroupStore = (*SQLiteGroupStore)(nil)

func NewSQLiteGroupStore(database *sql.DB) (*SQLiteGroupStore, error) {
	contents, err := assets.Contract(assets.SQLiteGroupStore)
	if err != nil {
		return nil, err
	}
	var queries sqliteGroupStoreContract
	if err := yaml.Unmarshal(contents, &queries); err != nil {
		return nil, err
	}
	if database == nil || queries.InsertApplicationGroup == "" || queries.InsertGroupPointer == "" || queries.SelectGroup == "" || queries.SelectGroups == "" || queries.SelectGroupKind == "" || queries.ArchiveApplicationGroup == "" || queries.InsertRevision == "" || queries.SelectRevision == "" || queries.SelectRevisionsFirstPage == "" || queries.SelectRevisionsAfterCursor == "" || queries.SelectRevisionExists == "" || queries.SelectGroupExists == "" || queries.SelectPointers == "" || queries.AdvancePointers == "" || queries.GroupNotFound == "" || queries.GroupAlreadyExists == "" || queries.RevisionNotFound == "" || queries.RevisionConflict == "" || queries.SystemGroupArchiveRejected == "" || queries.InvalidRevisionCursor == "" || queries.InvalidRevisionLimit == "" || queries.CursorSeparator == "" || queries.DefaultRevisionLimit < 1 || queries.MaximumRevisionLimit < queries.DefaultRevisionLimit || queries.ApplicationGroupKind == "" {
		return nil, errors.New(queries.InvalidContract)
	}
	return &SQLiteGroupStore{database: database, queries: queries}, nil
}

func (store *SQLiteGroupStore) CreateApplicationGroup(ctx context.Context, id string) (models.Group, error) {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.Group{}, err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, store.queries.InsertApplicationGroup, id)
	if err != nil {
		return models.Group{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return models.Group{}, err
	}
	if rows == 0 {
		return models.Group{}, models.GroupAlreadyExists{Message: store.queries.GroupAlreadyExists}
	}
	if _, err := transaction.ExecContext(ctx, store.queries.InsertGroupPointer, id); err != nil {
		return models.Group{}, err
	}
	group, err := scanGroup(transaction.QueryRowContext(ctx, store.queries.SelectGroup, id))
	if err != nil {
		return models.Group{}, err
	}
	if err := transaction.Commit(); err != nil {
		return models.Group{}, err
	}
	return group, nil
}

func (store *SQLiteGroupStore) GetGroup(ctx context.Context, id string) (models.Group, error) {
	group, err := scanGroup(store.database.QueryRowContext(ctx, store.queries.SelectGroup, id))
	if errors.Is(err, sql.ErrNoRows) {
		return models.Group{}, models.GroupNotFound{Message: store.queries.GroupNotFound}
	}
	return group, err
}

func (store *SQLiteGroupStore) ListGroups(ctx context.Context) (models.GroupList, error) {
	rows, err := store.database.QueryContext(ctx, store.queries.SelectGroups)
	if err != nil {
		return models.GroupList{}, err
	}
	defer rows.Close()
	groups := make([]models.Group, 0)
	for rows.Next() {
		group, err := scanGroup(rows)
		if err != nil {
			return models.GroupList{}, err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return models.GroupList{}, err
	}
	return models.GroupList{Items: groups}, nil
}

func (store *SQLiteGroupStore) ArchiveGroup(ctx context.Context, id string) (models.Group, error) {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.Group{}, err
	}
	defer transaction.Rollback()
	var kind string
	if err := transaction.QueryRowContext(ctx, store.queries.SelectGroupKind, id).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
		return models.Group{}, errors.New(store.queries.GroupNotFound)
	} else if err != nil {
		return models.Group{}, err
	}
	if kind != store.queries.ApplicationGroupKind {
		return models.Group{}, errors.New(store.queries.SystemGroupArchiveRejected)
	}
	if _, err := transaction.ExecContext(ctx, store.queries.ArchiveApplicationGroup, id); err != nil {
		return models.Group{}, err
	}
	group, err := scanGroup(transaction.QueryRowContext(ctx, store.queries.SelectGroup, id))
	if err != nil {
		return models.Group{}, err
	}
	if err := transaction.Commit(); err != nil {
		return models.Group{}, err
	}
	return group, nil
}

func (store *SQLiteGroupStore) CreateRevision(ctx context.Context, revision models.GroupRevision) (models.GroupRevision, error) {
	if _, err := store.database.ExecContext(ctx, store.queries.InsertRevision, revision.ID, revision.GroupID, revision.CaddyfileDigest, revision.ArtifactDigest, revision.CaddyfilePath, revision.ArtifactPath, revision.Actor); err != nil {
		return models.GroupRevision{}, err
	}
	return store.GetRevision(ctx, revision.GroupID, revision.ID)
}

func (store *SQLiteGroupStore) GetRevision(ctx context.Context, groupID string, revisionID string) (models.GroupRevision, error) {
	revision, err := scanRevision(store.database.QueryRowContext(ctx, store.queries.SelectRevision, groupID, revisionID))
	if errors.Is(err, sql.ErrNoRows) {
		return models.GroupRevision{}, errors.New(store.queries.RevisionNotFound)
	}
	return revision, err
}

func (store *SQLiteGroupStore) ListRevisions(ctx context.Context, groupID string, cursor string, limit int) (models.GroupRevisionList, error) {
	if limit == 0 {
		limit = store.queries.DefaultRevisionLimit
	}
	if limit < 1 || limit > store.queries.MaximumRevisionLimit {
		return models.GroupRevisionList{}, models.GroupRevisionPageError{Message: store.queries.InvalidRevisionLimit}
	}
	var groupExists bool
	if err := store.database.QueryRowContext(ctx, store.queries.SelectGroupExists, groupID).Scan(&groupExists); err != nil {
		return models.GroupRevisionList{}, err
	}
	if !groupExists {
		return models.GroupRevisionList{}, models.GroupNotFound{Message: store.queries.GroupNotFound}
	}
	var rows *sql.Rows
	var err error
	queryLimit := limit + 1
	if cursor == "" {
		rows, err = store.database.QueryContext(ctx, store.queries.SelectRevisionsFirstPage, groupID, queryLimit)
	} else {
		createdAt, revisionID, valid := decodeRevisionCursor(cursor, store.queries.CursorSeparator)
		if !valid {
			return models.GroupRevisionList{}, models.GroupRevisionPageError{Message: store.queries.InvalidRevisionCursor}
		}
		rows, err = store.database.QueryContext(ctx, store.queries.SelectRevisionsAfterCursor, groupID, createdAt, createdAt, revisionID, queryLimit)
	}
	if err != nil {
		return models.GroupRevisionList{}, err
	}
	defer rows.Close()
	revisions := make([]models.GroupRevision, 0, queryLimit)
	for rows.Next() {
		revision, err := scanRevision(rows)
		if err != nil {
			return models.GroupRevisionList{}, err
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return models.GroupRevisionList{}, err
	}
	page := models.GroupRevisionList{Items: revisions}
	if len(revisions) > limit {
		page.Items = revisions[:limit]
		tail := page.Items[len(page.Items)-1]
		nextCursor := encodeRevisionCursor(tail.CreatedAt, tail.ID, store.queries.CursorSeparator)
		page.NextCursor = &nextCursor
	}
	return page, nil
}

func (store *SQLiteGroupStore) GetPointers(ctx context.Context, groupID string) (models.GroupPointers, error) {
	pointers, err := scanPointers(store.database.QueryRowContext(ctx, store.queries.SelectPointers, groupID))
	if errors.Is(err, sql.ErrNoRows) {
		return models.GroupPointers{}, errors.New(store.queries.GroupNotFound)
	}
	return pointers, err
}

func (store *SQLiteGroupStore) AdvanceCurrent(ctx context.Context, groupID string, revisionID string, expectedCurrent *string) (models.GroupPointers, error) {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.GroupPointers{}, err
	}
	defer transaction.Rollback()
	var groupExists bool
	if err := transaction.QueryRowContext(ctx, store.queries.SelectGroupExists, groupID).Scan(&groupExists); err != nil {
		return models.GroupPointers{}, err
	}
	if !groupExists {
		return models.GroupPointers{}, errors.New(store.queries.GroupNotFound)
	}
	var revisionExists bool
	if err := transaction.QueryRowContext(ctx, store.queries.SelectRevisionExists, groupID, revisionID).Scan(&revisionExists); err != nil {
		return models.GroupPointers{}, err
	}
	if !revisionExists {
		return models.GroupPointers{}, errors.New(store.queries.RevisionNotFound)
	}
	result, err := transaction.ExecContext(ctx, store.queries.AdvancePointers, revisionID, groupID, expectedCurrent)
	if err != nil {
		return models.GroupPointers{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return models.GroupPointers{}, err
	}
	if updated != 1 {
		actual, readErr := scanPointers(transaction.QueryRowContext(ctx, store.queries.SelectPointers, groupID))
		if readErr != nil {
			return models.GroupPointers{}, readErr
		}
		return models.GroupPointers{}, models.GroupRevisionConflict{Expected: expectedCurrent, Actual: actual.CurrentRevisionID, Message: store.queries.RevisionConflict}
	}
	pointers, err := scanPointers(transaction.QueryRowContext(ctx, store.queries.SelectPointers, groupID))
	if err != nil {
		return models.GroupPointers{}, err
	}
	if err := transaction.Commit(); err != nil {
		return models.GroupPointers{}, err
	}
	return pointers, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanGroup(row rowScanner) (models.Group, error) {
	var group models.Group
	var active int
	var archivedAt sql.NullString
	var current sql.NullString
	var previous sql.NullString
	if err := row.Scan(&group.ID, &group.Kind, &active, &group.CreatedAt, &archivedAt, &current, &previous); err != nil {
		return models.Group{}, err
	}
	group.Active = active == 1
	if archivedAt.Valid {
		group.ArchivedAt = &archivedAt.String
	}
	if current.Valid {
		group.CurrentRevisionID = &current.String
	}
	if previous.Valid {
		group.PreviousRevisionID = &previous.String
	}
	return group, nil
}

func decodeRevisionCursor(cursor string, separator string) (string, string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", false
	}
	createdAt, revisionID, found := strings.Cut(string(decoded), separator)
	if !found || createdAt == "" || revisionID == "" || strings.Contains(revisionID, separator) {
		return "", "", false
	}
	return createdAt, revisionID, true
}

func encodeRevisionCursor(createdAt string, revisionID string, separator string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt + separator + revisionID))
}

func scanRevision(row rowScanner) (models.GroupRevision, error) {
	var revision models.GroupRevision
	var artifactDigest sql.NullString
	var artifactPath sql.NullString
	if err := row.Scan(&revision.ID, &revision.GroupID, &revision.CaddyfileDigest, &artifactDigest, &revision.CaddyfilePath, &artifactPath, &revision.CreatedAt, &revision.Actor); err != nil {
		return models.GroupRevision{}, err
	}
	if artifactDigest.Valid {
		revision.ArtifactDigest = &artifactDigest.String
	}
	if artifactPath.Valid {
		revision.ArtifactPath = &artifactPath.String
	}
	return revision, nil
}

func scanPointers(row rowScanner) (models.GroupPointers, error) {
	var pointers models.GroupPointers
	var current sql.NullString
	var previous sql.NullString
	if err := row.Scan(&pointers.GroupID, &current, &previous, &pointers.UpdatedAt); err != nil {
		return models.GroupPointers{}, err
	}
	if current.Valid {
		pointers.CurrentRevisionID = &current.String
	}
	if previous.Valid {
		pointers.PreviousRevisionID = &previous.String
	}
	return pointers, nil
}
