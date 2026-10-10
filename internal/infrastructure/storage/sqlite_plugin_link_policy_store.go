package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type pluginLinkPolicyStoreContract struct {
	InsertPolicy       string
	SelectPolicy       string
	SelectPolicyExists string
	SelectAllPolicies  string
	UpdatePolicy       string
	DeletePolicy       string
	TimestampLayout    string
	Diagnostics        struct {
		InvalidContract string
		InvalidStore    string
	}
}

// SQLitePluginLinkPolicyStore persists Core-owned caller→target peer link
// policy. Each row carries a monotonic revision used for optimistic
// concurrency, and every mutation commits its audit record in the same
// transaction.
type SQLitePluginLinkPolicyStore struct {
	database *sql.DB
	contract pluginLinkPolicyStoreContract
	audit    *SQLiteAuditStore
}

var _ interfaces.PluginLinkPolicyStore = (*SQLitePluginLinkPolicyStore)(nil)

type pluginLinkRuleJSON struct {
	PlacementRule     string                  `json:"placementRule"`
	Carrier           string                  `json:"carrier"`
	Weight            uint16                  `json:"weight"`
	RequiredContracts []peerContractRangeJSON `json:"requiredContracts"`
}

type peerContractRangeJSON struct {
	ContractID              string `json:"contractId"`
	MinimumVersion          string `json:"minimumVersion"`
	MaximumVersionExclusive string `json:"maximumVersionExclusive"`
}

func NewSQLitePluginLinkPolicyStore(database *sql.DB) (*SQLitePluginLinkPolicyStore, error) {
	contract := linkPolicyDefinitions()
	if database == nil || !validPluginLinkPolicyContract(contract) {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	return &SQLitePluginLinkPolicyStore{database: database, contract: contract, audit: audit}, nil
}

func (store *SQLitePluginLinkPolicyStore) List(ctx context.Context) ([]models.PluginLinkPolicy, error) {
	if store == nil || store.database == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := store.database.QueryContext(ctx, store.contract.SelectAllPolicies)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := make([]models.PluginLinkPolicy, 0)
	for rows.Next() {
		var callerInstanceID, targetInstanceID string
		var revision int64
		var rulesJSON []byte
		if err := rows.Scan(&callerInstanceID, &targetInstanceID, &revision, &rulesJSON); err != nil {
			return nil, err
		}
		policy, err := decodePluginLinkPolicy(callerInstanceID, targetInstanceID, revision, rulesJSON, store.contract.Diagnostics.InvalidStore)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return policies, nil
}

func (store *SQLitePluginLinkPolicyStore) Get(ctx context.Context, callerInstanceID, targetInstanceID string) (models.PluginLinkPolicy, error) {
	if store == nil || store.database == nil {
		return models.PluginLinkPolicy{}, sql.ErrConnDone
	}
	var revision int64
	var rulesJSON []byte
	err := store.database.QueryRowContext(ctx, store.contract.SelectPolicy, callerInstanceID, targetInstanceID).Scan(&revision, &rulesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return models.PluginLinkPolicy{}, models.PeerLinkPolicyNotFound{}
	}
	if err != nil {
		return models.PluginLinkPolicy{}, err
	}
	return decodePluginLinkPolicy(callerInstanceID, targetInstanceID, revision, rulesJSON, store.contract.Diagnostics.InvalidStore)
}

func (store *SQLitePluginLinkPolicyStore) Create(ctx context.Context, policy models.PluginLinkPolicy, audit models.AuditRecord) (models.PluginLinkPolicy, error) {
	if store == nil || store.database == nil {
		return models.PluginLinkPolicy{}, sql.ErrConnDone
	}
	if policy.Revision != 0 || policy.Validate() != nil {
		return models.PluginLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	rulesJSON, err := encodePluginLinkRules(policy.Rules)
	if err != nil {
		return models.PluginLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	now := time.Now().UTC().Format(store.contract.TimestampLayout)
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.PluginLinkPolicy{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, store.contract.InsertPolicy,
		policy.CallerInstanceID, policy.TargetInstanceID, int64(1), rulesJSON, now, now)
	if err != nil {
		if isUniqueConstraint(err) {
			return models.PluginLinkPolicy{}, models.PeerLinkPolicyConflict{}
		}
		return models.PluginLinkPolicy{}, err
	}
	if err := requireOneRow(result); err != nil {
		return models.PluginLinkPolicy{}, errors.New(store.contract.Diagnostics.InvalidStore)
	}
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return models.PluginLinkPolicy{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.PluginLinkPolicy{}, err
	}
	policy.Revision = 1
	return policy, nil
}

func (store *SQLitePluginLinkPolicyStore) Replace(
	ctx context.Context,
	callerInstanceID, targetInstanceID string,
	expectedRevision int64,
	policy models.PluginLinkPolicy,
	audit models.AuditRecord,
) (models.PluginLinkPolicy, error) {
	if store == nil || store.database == nil {
		return models.PluginLinkPolicy{}, sql.ErrConnDone
	}
	if expectedRevision < 1 || policy.Revision != 0 || policy.Validate() != nil ||
		policy.CallerInstanceID != callerInstanceID || policy.TargetInstanceID != targetInstanceID {
		return models.PluginLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	rulesJSON, err := encodePluginLinkRules(policy.Rules)
	if err != nil {
		return models.PluginLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	next := expectedRevision + 1
	now := time.Now().UTC().Format(store.contract.TimestampLayout)
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.PluginLinkPolicy{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, store.contract.UpdatePolicy,
		next, rulesJSON, now, callerInstanceID, targetInstanceID, expectedRevision)
	if err != nil {
		return models.PluginLinkPolicy{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return models.PluginLinkPolicy{}, err
	}
	if changed == 0 {
		return models.PluginLinkPolicy{}, store.linkPolicyConcurrencyMiss(ctx, tx, callerInstanceID, targetInstanceID)
	}
	if changed != 1 {
		return models.PluginLinkPolicy{}, errors.New(store.contract.Diagnostics.InvalidStore)
	}
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return models.PluginLinkPolicy{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.PluginLinkPolicy{}, err
	}
	policy.Revision = next
	return policy, nil
}

func (store *SQLitePluginLinkPolicyStore) Delete(
	ctx context.Context,
	callerInstanceID, targetInstanceID string,
	expectedRevision int64,
	audit models.AuditRecord,
) error {
	if store == nil || store.database == nil {
		return sql.ErrConnDone
	}
	if expectedRevision < 1 {
		return models.PeerLinkPolicyInvalid{}
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, store.contract.DeletePolicy, callerInstanceID, targetInstanceID, expectedRevision)
	if err != nil {
		return err
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if removed == 0 {
		return store.linkPolicyConcurrencyMiss(ctx, tx, callerInstanceID, targetInstanceID)
	}
	if removed != 1 {
		return errors.New(store.contract.Diagnostics.InvalidStore)
	}
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit()
}

// linkPolicyConcurrencyMiss distinguishes a missing policy from a stale
// If-Match revision so the handler can return 404 or 409 without widening
// access.
func (store *SQLitePluginLinkPolicyStore) linkPolicyConcurrencyMiss(ctx context.Context, tx *sql.Tx, callerInstanceID, targetInstanceID string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, store.contract.SelectPolicyExists, callerInstanceID, targetInstanceID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return models.PeerLinkPolicyNotFound{}
	}
	return models.PeerLinkPolicyConflict{}
}

func encodePluginLinkRules(rules []models.PeerLinkRule) ([]byte, error) {
	encoded := make([]pluginLinkRuleJSON, 0, len(rules))
	for _, rule := range rules {
		requirements := make([]peerContractRangeJSON, 0, len(rule.RequiredContracts))
		for _, requirement := range rule.RequiredContracts {
			requirements = append(requirements, peerContractRangeJSON{
				ContractID:              requirement.ContractID,
				MinimumVersion:          requirement.MinimumVersion,
				MaximumVersionExclusive: requirement.MaximumVersionExclusive,
			})
		}
		encoded = append(encoded, pluginLinkRuleJSON{
			PlacementRule:     rule.PlacementRule,
			Carrier:           rule.Carrier,
			Weight:            rule.Weight,
			RequiredContracts: requirements,
		})
	}
	return json.Marshal(encoded)
}

func decodePluginLinkPolicy(callerInstanceID, targetInstanceID string, revision int64, rulesJSON []byte, invalidStore string) (models.PluginLinkPolicy, error) {
	var decoded []pluginLinkRuleJSON
	if err := json.Unmarshal(rulesJSON, &decoded); err != nil {
		return models.PluginLinkPolicy{}, errors.New(invalidStore)
	}
	rules := make([]models.PeerLinkRule, 0, len(decoded))
	for _, rule := range decoded {
		requirements := make([]models.PeerContractRange, 0, len(rule.RequiredContracts))
		for _, requirement := range rule.RequiredContracts {
			requirements = append(requirements, models.PeerContractRange{
				ContractID:              requirement.ContractID,
				MinimumVersion:          requirement.MinimumVersion,
				MaximumVersionExclusive: requirement.MaximumVersionExclusive,
			})
		}
		rules = append(rules, models.PeerLinkRule{
			CallerInstanceID:  callerInstanceID,
			TargetInstanceID:  targetInstanceID,
			PlacementRule:     rule.PlacementRule,
			Carrier:           rule.Carrier,
			Weight:            rule.Weight,
			RequiredContracts: requirements,
		})
	}
	policy := models.PluginLinkPolicy{
		CallerInstanceID: callerInstanceID,
		TargetInstanceID: targetInstanceID,
		Revision:         revision,
		Rules:            rules,
	}
	// Fail closed: a corrupted Core-owned row must never publish a policy that
	// does not satisfy the generic authoring rules.
	if revision < 1 || policy.Validate() != nil {
		return models.PluginLinkPolicy{}, errors.New(invalidStore)
	}
	return policy, nil
}

func validPluginLinkPolicyContract(contract pluginLinkPolicyStoreContract) bool {
	return contract.InsertPolicy != "" && contract.SelectPolicy != "" && contract.SelectPolicyExists != "" &&
		contract.SelectAllPolicies != "" && contract.UpdatePolicy != "" && contract.DeletePolicy != "" &&
		contract.TimestampLayout != "" && contract.Diagnostics.InvalidContract != "" &&
		contract.Diagnostics.InvalidStore != ""
}
