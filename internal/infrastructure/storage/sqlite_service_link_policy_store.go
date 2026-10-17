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

type serviceLinkPolicyStoreContract struct {
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

// SQLiteServiceLinkPolicyStore persists Core-owned caller→target peer link
// policy. Each row carries a monotonic revision used for optimistic
// concurrency, and every mutation commits its audit record in the same
// transaction.
type SQLiteServiceLinkPolicyStore struct {
	database *sql.DB
	contract serviceLinkPolicyStoreContract
	audit    *SQLiteAuditStore
}

var _ interfaces.ServiceLinkPolicyStore = (*SQLiteServiceLinkPolicyStore)(nil)

type serviceLinkRuleJSON struct {
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

func NewSQLiteServiceLinkPolicyStore(database *sql.DB) (*SQLiteServiceLinkPolicyStore, error) {
	contract := linkPolicyDefinitions()
	if database == nil || !validServiceLinkPolicyContract(contract) {
		return nil, errors.New(contract.Diagnostics.InvalidContract)
	}
	audit, err := NewSQLiteAuditStore(database)
	if err != nil {
		return nil, err
	}
	return &SQLiteServiceLinkPolicyStore{database: database, contract: contract, audit: audit}, nil
}

func (store *SQLiteServiceLinkPolicyStore) List(ctx context.Context) ([]models.ServiceLinkPolicy, error) {
	if store == nil || store.database == nil {
		return nil, sql.ErrConnDone
	}
	rows, err := store.database.QueryContext(ctx, store.contract.SelectAllPolicies)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := make([]models.ServiceLinkPolicy, 0)
	for rows.Next() {
		var callerInstanceID, targetInstanceID string
		var revision int64
		var rulesJSON []byte
		if err := rows.Scan(&callerInstanceID, &targetInstanceID, &revision, &rulesJSON); err != nil {
			return nil, err
		}
		policy, err := decodeServiceLinkPolicy(callerInstanceID, targetInstanceID, revision, rulesJSON, store.contract.Diagnostics.InvalidStore)
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

func (store *SQLiteServiceLinkPolicyStore) Get(ctx context.Context, callerInstanceID, targetInstanceID string) (models.ServiceLinkPolicy, error) {
	if store == nil || store.database == nil {
		return models.ServiceLinkPolicy{}, sql.ErrConnDone
	}
	var revision int64
	var rulesJSON []byte
	err := store.database.QueryRowContext(ctx, store.contract.SelectPolicy, callerInstanceID, targetInstanceID).Scan(&revision, &rulesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return models.ServiceLinkPolicy{}, models.PeerLinkPolicyNotFound{}
	}
	if err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	return decodeServiceLinkPolicy(callerInstanceID, targetInstanceID, revision, rulesJSON, store.contract.Diagnostics.InvalidStore)
}

func (store *SQLiteServiceLinkPolicyStore) Create(ctx context.Context, policy models.ServiceLinkPolicy, audit models.AuditRecord) (models.ServiceLinkPolicy, error) {
	if store == nil || store.database == nil {
		return models.ServiceLinkPolicy{}, sql.ErrConnDone
	}
	if policy.Revision != 0 || policy.Validate() != nil {
		return models.ServiceLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	rulesJSON, err := encodeServiceLinkRules(policy.Rules)
	if err != nil {
		return models.ServiceLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	now := time.Now().UTC().Format(store.contract.TimestampLayout)
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, store.contract.InsertPolicy,
		policy.CallerInstanceID, policy.TargetInstanceID, int64(1), rulesJSON, now, now)
	if err != nil {
		if isUniqueConstraint(err) {
			return models.ServiceLinkPolicy{}, models.PeerLinkPolicyConflict{}
		}
		return models.ServiceLinkPolicy{}, err
	}
	if err := requireOneRow(result); err != nil {
		return models.ServiceLinkPolicy{}, errors.New(store.contract.Diagnostics.InvalidStore)
	}
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	policy.Revision = 1
	return policy, nil
}

func (store *SQLiteServiceLinkPolicyStore) Replace(
	ctx context.Context,
	callerInstanceID, targetInstanceID string,
	expectedRevision int64,
	policy models.ServiceLinkPolicy,
	audit models.AuditRecord,
) (models.ServiceLinkPolicy, error) {
	if store == nil || store.database == nil {
		return models.ServiceLinkPolicy{}, sql.ErrConnDone
	}
	if expectedRevision < 1 || policy.Revision != 0 || policy.Validate() != nil ||
		policy.CallerInstanceID != callerInstanceID || policy.TargetInstanceID != targetInstanceID {
		return models.ServiceLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	rulesJSON, err := encodeServiceLinkRules(policy.Rules)
	if err != nil {
		return models.ServiceLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	next := expectedRevision + 1
	now := time.Now().UTC().Format(store.contract.TimestampLayout)
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, store.contract.UpdatePolicy,
		next, rulesJSON, now, callerInstanceID, targetInstanceID, expectedRevision)
	if err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	if changed == 0 {
		return models.ServiceLinkPolicy{}, store.linkPolicyConcurrencyMiss(ctx, tx, callerInstanceID, targetInstanceID)
	}
	if changed != 1 {
		return models.ServiceLinkPolicy{}, errors.New(store.contract.Diagnostics.InvalidStore)
	}
	if err := store.audit.append(ctx, tx, audit); err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	policy.Revision = next
	return policy, nil
}

func (store *SQLiteServiceLinkPolicyStore) Delete(
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
func (store *SQLiteServiceLinkPolicyStore) linkPolicyConcurrencyMiss(ctx context.Context, tx *sql.Tx, callerInstanceID, targetInstanceID string) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, store.contract.SelectPolicyExists, callerInstanceID, targetInstanceID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return models.PeerLinkPolicyNotFound{}
	}
	return models.PeerLinkPolicyConflict{}
}

func encodeServiceLinkRules(rules []models.PeerLinkRule) ([]byte, error) {
	encoded := make([]serviceLinkRuleJSON, 0, len(rules))
	for _, rule := range rules {
		requirements := make([]peerContractRangeJSON, 0, len(rule.RequiredContracts))
		for _, requirement := range rule.RequiredContracts {
			requirements = append(requirements, peerContractRangeJSON{
				ContractID:              requirement.ContractID,
				MinimumVersion:          requirement.MinimumVersion,
				MaximumVersionExclusive: requirement.MaximumVersionExclusive,
			})
		}
		encoded = append(encoded, serviceLinkRuleJSON{
			PlacementRule:     rule.PlacementRule,
			Carrier:           rule.Carrier,
			Weight:            rule.Weight,
			RequiredContracts: requirements,
		})
	}
	return json.Marshal(encoded)
}

func decodeServiceLinkPolicy(callerInstanceID, targetInstanceID string, revision int64, rulesJSON []byte, invalidStore string) (models.ServiceLinkPolicy, error) {
	var decoded []serviceLinkRuleJSON
	if err := json.Unmarshal(rulesJSON, &decoded); err != nil {
		return models.ServiceLinkPolicy{}, errors.New(invalidStore)
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
	policy := models.ServiceLinkPolicy{
		CallerInstanceID: callerInstanceID,
		TargetInstanceID: targetInstanceID,
		Revision:         revision,
		Rules:            rules,
	}
	// Fail closed: a corrupted Core-owned row must never publish a policy that
	// does not satisfy the generic authoring rules.
	if revision < 1 || policy.Validate() != nil {
		return models.ServiceLinkPolicy{}, errors.New(invalidStore)
	}
	return policy, nil
}

func validServiceLinkPolicyContract(contract serviceLinkPolicyStoreContract) bool {
	return contract.InsertPolicy != "" && contract.SelectPolicy != "" && contract.SelectPolicyExists != "" &&
		contract.SelectAllPolicies != "" && contract.UpdatePolicy != "" && contract.DeletePolicy != "" &&
		contract.TimestampLayout != "" && contract.Diagnostics.InvalidContract != "" &&
		contract.Diagnostics.InvalidStore != ""
}
