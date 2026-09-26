package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	assets "github.com/Liapoldus/core"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"gopkg.in/yaml.v3"
)

type sqlitePluginCookiePolicyContract struct {
	SelectPolicy              string `yaml:"selectPolicy"`
	SelectAllPolicies         string `yaml:"selectAllPolicies"`
	SelectSupportedCapability string `yaml:"selectSupportedCapability"`
	InsertPolicy              string `yaml:"insertPolicy"`
	UpdatePolicy              string `yaml:"updatePolicy"`
	NotFound                  string `yaml:"notFound"`
	RevisionConflict          string `yaml:"revisionConflict"`
	InvalidContract           string `yaml:"invalidContract"`
	InvalidPolicy             string `yaml:"invalidPolicy"`
	WriteFailure              string `yaml:"writeFailure"`
	TimestampLayout           string `yaml:"timestampLayout"`
}

type SQLitePluginCookiePolicyStore struct {
	database *sql.DB
	audit    *SQLiteAuditStore
	contract sqlitePluginCookiePolicyContract
}

var _ interfaces.PluginCookiePolicyStore = (*SQLitePluginCookiePolicyStore)(nil)

func NewSQLitePluginCookiePolicyStore(database *sql.DB) (*SQLitePluginCookiePolicyStore, error) {
	contents, err := assets.Contract(assets.SQLitePluginCookiePolicies)
	if err != nil {
		return nil, err
	}
	var contract sqlitePluginCookiePolicyContract
	if err := yaml.Unmarshal(contents, &contract); err != nil {
		return nil, err
	}
	if database == nil || contract.SelectPolicy == "" || contract.SelectAllPolicies == "" || contract.SelectSupportedCapability == "" || contract.InsertPolicy == "" || contract.UpdatePolicy == "" || contract.NotFound == "" || contract.RevisionConflict == "" || contract.InvalidContract == "" || contract.InvalidPolicy == "" || contract.WriteFailure == "" || contract.TimestampLayout == "" {
		return nil, errors.New(contract.InvalidContract)
	}
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	return &SQLitePluginCookiePolicyStore{database: database, audit: audit, contract: contract}, nil
}

func (store *SQLitePluginCookiePolicyStore) List(ctx context.Context) ([]models.PluginCookiePolicy, error) {
	rows, err := store.database.QueryContext(ctx, store.contract.SelectAllPolicies)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := make([]models.PluginCookiePolicy, 0)
	for rows.Next() {
		var policy models.PluginCookiePolicy
		var encoded []byte
		if err := rows.Scan(&policy.InstanceID, &policy.Capability, &encoded, &policy.Revision); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(encoded, &policy.AllowedNames); err != nil || policy.Revision < 1 || policy.AllowedNames == nil {
			return nil, models.PluginCookiePolicyNotFound{Message: store.contract.InvalidPolicy}
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return policies, nil
}

func (store *SQLitePluginCookiePolicyStore) Get(ctx context.Context, instanceID, capability string) (models.PluginCookiePolicy, error) {
	if instanceID == "" || capability == "" {
		return models.PluginCookiePolicy{}, models.PluginCookiePolicyNotFound{Message: store.contract.NotFound}
	}
	if err := store.requireSupportedCapability(ctx, store.database, instanceID, capability); err != nil {
		return models.PluginCookiePolicy{}, err
	}
	policy := models.PluginCookiePolicy{InstanceID: instanceID, Capability: capability, AllowedNames: []string{}}
	var encoded []byte
	if err := store.database.QueryRowContext(ctx, store.contract.SelectPolicy, instanceID, capability).Scan(&encoded, &policy.Revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return policy, nil
		}
		return models.PluginCookiePolicy{}, err
	}
	if err := json.Unmarshal(encoded, &policy.AllowedNames); err != nil || policy.AllowedNames == nil || policy.Revision < 1 {
		return models.PluginCookiePolicy{}, models.PluginCookiePolicyNotFound{Message: store.contract.InvalidPolicy}
	}
	return policy, nil
}

func (store *SQLitePluginCookiePolicyStore) CompareAndSwap(ctx context.Context, expectedRevision int64, policy models.PluginCookiePolicy, record models.AuditRecord) (models.PluginCookiePolicy, error) {
	if policy.InstanceID == "" || policy.Capability == "" || expectedRevision < 0 || record.Actor == "" || record.Action == "" || record.Resource == "" || record.Result == "" || record.RequestID == "" {
		return models.PluginCookiePolicy{}, errors.New(store.contract.InvalidPolicy)
	}
	if policy.AllowedNames == nil {
		policy.AllowedNames = []string{}
	}
	encoded, err := json.Marshal(policy.AllowedNames)
	if err != nil {
		return models.PluginCookiePolicy{}, errors.New(store.contract.InvalidPolicy)
	}
	nextRevision := expectedRevision + 1
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.PluginCookiePolicy{}, err
	}
	defer transaction.Rollback()
	if err := store.requireSupportedCapability(ctx, transaction, policy.InstanceID, policy.Capability); err != nil {
		return models.PluginCookiePolicy{}, err
	}
	updatedAt := time.Now().UTC().Format(store.contract.TimestampLayout)
	var result sql.Result
	if expectedRevision == 0 {
		result, err = transaction.ExecContext(ctx, store.contract.InsertPolicy, policy.InstanceID, policy.Capability, encoded, nextRevision, updatedAt)
	} else {
		result, err = transaction.ExecContext(ctx, store.contract.UpdatePolicy, encoded, nextRevision, updatedAt, policy.InstanceID, policy.Capability, expectedRevision)
	}
	if err != nil {
		return models.PluginCookiePolicy{}, errors.New(store.contract.WriteFailure)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return models.PluginCookiePolicy{}, err
	}
	if rows != 1 {
		return models.PluginCookiePolicy{}, models.PluginCookiePolicyRevisionConflict{Message: store.contract.RevisionConflict}
	}
	if err := store.audit.append(ctx, transaction, record); err != nil {
		return models.PluginCookiePolicy{}, err
	}
	if err := transaction.Commit(); err != nil {
		return models.PluginCookiePolicy{}, err
	}
	policy.Revision = nextRevision
	return policy, nil
}

type cookiePolicyQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (store *SQLitePluginCookiePolicyStore) requireSupportedCapability(ctx context.Context, queryer cookiePolicyQueryer, instanceID, capability string) error {
	var supported bool
	if err := queryer.QueryRowContext(ctx, store.contract.SelectSupportedCapability, instanceID, capability).Scan(&supported); err != nil {
		return err
	}
	if !supported {
		return models.PluginCookiePolicyNotFound{Message: store.contract.NotFound}
	}
	return nil
}
