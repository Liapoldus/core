package storage

import (
	"context"
	"database/sql"
	"errors"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type sqliteAccessContract struct {
	BeginImmediate        string `yaml:"beginImmediate"`
	Commit                string `yaml:"commit"`
	Rollback              string `yaml:"rollback"`
	SelectActiveKey       string `yaml:"selectActiveKey"`
	InsertKey             string `yaml:"insertKey"`
	SelectActiveVerifiers string `yaml:"selectActiveVerifiers"`
	BootstrapName         string `yaml:"bootstrapName"`
	InvalidContract       string `yaml:"invalidContract"`
	ActiveKeyConflict     string `yaml:"activeKeyConflict"`
}

type SQLiteServiceKeyStore struct {
	database *sql.DB
	contract sqliteAccessContract
}

var _ interfaces.ServiceKeyStore = (*SQLiteServiceKeyStore)(nil)

func NewSQLiteServiceKeyStore(database *sql.DB) (*SQLiteServiceKeyStore, error) {
	contents, err := assets.Contract(assets.SQLiteAccess)
	if err != nil {
		return nil, err
	}
	var contract sqliteAccessContract
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		return nil, err
	}
	if database == nil || contract.BeginImmediate == "" || contract.Commit == "" || contract.Rollback == "" || contract.SelectActiveKey == "" || contract.InsertKey == "" || contract.SelectActiveVerifiers == "" || contract.BootstrapName == "" || contract.InvalidContract == "" || contract.ActiveKeyConflict == "" {
		return nil, errors.New(contract.InvalidContract)
	}
	return &SQLiteServiceKeyStore{database: database, contract: contract}, nil
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
