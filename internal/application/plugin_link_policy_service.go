package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// PluginLinkPolicyService owns the Core link policy use cases. Every mutation
// is validated, idempotency-reserved, committed with its audit record in one
// SQLite transaction, and then refreshes the immutable in-memory snapshot that
// the peer directory serves.
type PluginLinkPolicyService struct {
	Store      interfaces.PluginLinkPolicyStore
	Reader     interfaces.PluginLinkPolicyReader
	Operations interfaces.OperationStore
	Kinds      PluginLinkPolicyOperationKinds
	States     PluginLinkPolicyOperationStates
	Now        func() time.Time
	Refresh    func()
}

type PluginLinkPolicyOperationKinds struct {
	Create  string
	Replace string
	Delete  string
}

type PluginLinkPolicyOperationStates struct {
	Pending   string
	Succeeded string
	Failed    string
}

var errPluginLinkPolicyServiceMisconfigured = errors.New("plugin link policy service is misconfigured")

func (service *PluginLinkPolicyService) Create(
	ctx context.Context,
	policy models.PluginLinkPolicy,
	audit models.AuditRecord,
	actor, requestID, scope, key, digest string,
) (models.PluginLinkPolicy, error) {
	if err := service.ready(); err != nil {
		return models.PluginLinkPolicy{}, err
	}
	if policy.Revision != 0 || policy.Validate() != nil {
		return models.PluginLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	operation, created, err := service.reserve(ctx, service.Kinds.Create, actor, requestID, policy.CallerInstanceID, scope, key, digest)
	if err != nil {
		return models.PluginLinkPolicy{}, err
	}
	if !created {
		return service.Reader.Get(ctx, policy.CallerInstanceID, policy.TargetInstanceID)
	}
	createdPolicy, err := service.Store.Create(ctx, policy, audit)
	if err != nil {
		service.fail(ctx, operation)
		return models.PluginLinkPolicy{}, err
	}
	service.complete(ctx, operation)
	service.refresh()
	return createdPolicy, nil
}

func (service *PluginLinkPolicyService) Replace(
	ctx context.Context,
	policy models.PluginLinkPolicy,
	expectedRevision int64,
	audit models.AuditRecord,
	actor, requestID, scope, key, digest string,
) (models.PluginLinkPolicy, error) {
	if err := service.ready(); err != nil {
		return models.PluginLinkPolicy{}, err
	}
	if expectedRevision < 1 || policy.Revision != 0 || policy.Validate() != nil {
		return models.PluginLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	operation, created, err := service.reserve(ctx, service.Kinds.Replace, actor, requestID, policy.CallerInstanceID, scope, key, digest)
	if err != nil {
		return models.PluginLinkPolicy{}, err
	}
	if !created {
		return service.Reader.Get(ctx, policy.CallerInstanceID, policy.TargetInstanceID)
	}
	replaced, err := service.Store.Replace(ctx, policy.CallerInstanceID, policy.TargetInstanceID, expectedRevision, policy, audit)
	if err != nil {
		service.fail(ctx, operation)
		return models.PluginLinkPolicy{}, err
	}
	service.complete(ctx, operation)
	service.refresh()
	return replaced, nil
}

func (service *PluginLinkPolicyService) Delete(
	ctx context.Context,
	callerInstanceID, targetInstanceID string,
	expectedRevision int64,
	audit models.AuditRecord,
	actor, requestID, scope, key, digest string,
) error {
	if err := service.ready(); err != nil {
		return err
	}
	if expectedRevision < 1 || callerInstanceID == "" || targetInstanceID == "" {
		return models.PeerLinkPolicyInvalid{}
	}
	operation, created, err := service.reserve(ctx, service.Kinds.Delete, actor, requestID, callerInstanceID, scope, key, digest)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}
	if err := service.Store.Delete(ctx, callerInstanceID, targetInstanceID, expectedRevision, audit); err != nil {
		service.fail(ctx, operation)
		return err
	}
	service.complete(ctx, operation)
	service.refresh()
	return nil
}

func (service *PluginLinkPolicyService) List(ctx context.Context) ([]models.PluginLinkPolicy, error) {
	return service.Reader.List(ctx)
}

func (service *PluginLinkPolicyService) Get(ctx context.Context, callerInstanceID, targetInstanceID string) (models.PluginLinkPolicy, error) {
	return service.Reader.Get(ctx, callerInstanceID, targetInstanceID)
}

// reserve enforces the Idempotency-Key contract. It returns created=false when
// an earlier request with the same key and digest already reserved the
// mutation, so the caller can replay the stored outcome instead of re-applying
// it. A reused key with a different digest surfaces as an idempotency conflict.
func (service *PluginLinkPolicyService) reserve(
	ctx context.Context,
	kind, actor, requestID, resource, scope, key, digest string,
) (models.Operation, bool, error) {
	existing, found, err := service.Operations.FindByIdempotency(ctx, actor, scope, key, digest)
	if err != nil {
		return models.Operation{}, false, err
	}
	if found {
		return existing, false, nil
	}
	operation := models.Operation{
		ID:        newPluginLinkOperationID(),
		Kind:      kind,
		State:     service.States.Pending,
		CreatedAt: service.Now().UTC(),
		RequestID: requestID,
		Actor:     actor,
		Resource:  resource,
	}
	reserved, created, err := service.Operations.Reserve(ctx, models.OperationReservation{
		Operation:     operation,
		Scope:         scope,
		Key:           key,
		RequestDigest: digest,
	})
	if err != nil {
		return models.Operation{}, false, err
	}
	return reserved, created, nil
}

func (service *PluginLinkPolicyService) complete(ctx context.Context, operation models.Operation) {
	_ = service.Operations.Transition(ctx, operation.ID, service.States.Pending, service.States.Succeeded, "")
}

func (service *PluginLinkPolicyService) fail(ctx context.Context, operation models.Operation) {
	_ = service.Operations.Transition(ctx, operation.ID, service.States.Pending, service.States.Failed, "")
}

func (service *PluginLinkPolicyService) refresh() {
	if service.Refresh != nil {
		service.Refresh()
	}
}

func (service *PluginLinkPolicyService) ready() error {
	if service == nil || service.Store == nil || service.Reader == nil || service.Operations == nil ||
		service.Now == nil || service.Kinds.Create == "" || service.Kinds.Replace == "" || service.Kinds.Delete == "" ||
		service.States.Pending == "" || service.States.Succeeded == "" || service.States.Failed == "" {
		return errPluginLinkPolicyServiceMisconfigured
	}
	return nil
}

func newPluginLinkOperationID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "plugin-link-operation"
	}
	return "op_" + hex.EncodeToString(buffer)
}
