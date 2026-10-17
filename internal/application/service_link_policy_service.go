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

// ServiceLinkPolicyService owns the Core link policy use cases. Every mutation
// is validated, idempotency-reserved, committed with its audit record in one
// SQLite transaction, and then refreshes the immutable in-memory snapshot that
// the peer directory serves.
type ServiceLinkPolicyService struct {
	Store      interfaces.ServiceLinkPolicyStore
	Reader     interfaces.ServiceLinkPolicyReader
	Operations interfaces.OperationStore
	Kinds      ServiceLinkPolicyOperationKinds
	States     ServiceLinkPolicyOperationStates
	Now        func() time.Time
	Refresh    func()
}

type ServiceLinkPolicyOperationKinds struct {
	Create  string
	Replace string
	Delete  string
}

type ServiceLinkPolicyOperationStates struct {
	Pending   string
	Succeeded string
	Failed    string
}

var errServiceLinkPolicyServiceMisconfigured = errors.New("service link policy service is misconfigured")

func (service *ServiceLinkPolicyService) Create(
	ctx context.Context,
	policy models.ServiceLinkPolicy,
	audit models.AuditRecord,
	actor, requestID, scope, key, digest string,
) (models.ServiceLinkPolicy, error) {
	if err := service.ready(); err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	if policy.Revision != 0 || policy.Validate() != nil {
		return models.ServiceLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	operation, created, err := service.reserve(ctx, service.Kinds.Create, actor, requestID, policy.CallerInstanceID, scope, key, digest)
	if err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	if !created {
		return service.Reader.Get(ctx, policy.CallerInstanceID, policy.TargetInstanceID)
	}
	createdPolicy, err := service.Store.Create(ctx, policy, audit)
	if err != nil {
		service.fail(ctx, operation)
		return models.ServiceLinkPolicy{}, err
	}
	service.complete(ctx, operation)
	service.refresh()
	return createdPolicy, nil
}

func (service *ServiceLinkPolicyService) Replace(
	ctx context.Context,
	policy models.ServiceLinkPolicy,
	expectedRevision int64,
	audit models.AuditRecord,
	actor, requestID, scope, key, digest string,
) (models.ServiceLinkPolicy, error) {
	if err := service.ready(); err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	if expectedRevision < 1 || policy.Revision != 0 || policy.Validate() != nil {
		return models.ServiceLinkPolicy{}, models.PeerLinkPolicyInvalid{}
	}
	operation, created, err := service.reserve(ctx, service.Kinds.Replace, actor, requestID, policy.CallerInstanceID, scope, key, digest)
	if err != nil {
		return models.ServiceLinkPolicy{}, err
	}
	if !created {
		return service.Reader.Get(ctx, policy.CallerInstanceID, policy.TargetInstanceID)
	}
	replaced, err := service.Store.Replace(ctx, policy.CallerInstanceID, policy.TargetInstanceID, expectedRevision, policy, audit)
	if err != nil {
		service.fail(ctx, operation)
		return models.ServiceLinkPolicy{}, err
	}
	service.complete(ctx, operation)
	service.refresh()
	return replaced, nil
}

func (service *ServiceLinkPolicyService) Delete(
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

func (service *ServiceLinkPolicyService) List(ctx context.Context) ([]models.ServiceLinkPolicy, error) {
	return service.Reader.List(ctx)
}

func (service *ServiceLinkPolicyService) Get(ctx context.Context, callerInstanceID, targetInstanceID string) (models.ServiceLinkPolicy, error) {
	return service.Reader.Get(ctx, callerInstanceID, targetInstanceID)
}

// reserve enforces the Idempotency-Key contract. It returns created=false when
// an earlier request with the same key and digest already reserved the
// mutation, so the caller can replay the stored outcome instead of re-applying
// it. A reused key with a different digest surfaces as an idempotency conflict.
func (service *ServiceLinkPolicyService) reserve(
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
		ID:        newServiceLinkOperationID(),
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

func (service *ServiceLinkPolicyService) complete(ctx context.Context, operation models.Operation) {
	_ = service.Operations.Transition(ctx, operation.ID, service.States.Pending, service.States.Succeeded, "")
}

func (service *ServiceLinkPolicyService) fail(ctx context.Context, operation models.Operation) {
	_ = service.Operations.Transition(ctx, operation.ID, service.States.Pending, service.States.Failed, "")
}

func (service *ServiceLinkPolicyService) refresh() {
	if service.Refresh != nil {
		service.Refresh()
	}
}

func (service *ServiceLinkPolicyService) ready() error {
	if service == nil || service.Store == nil || service.Reader == nil || service.Operations == nil ||
		service.Now == nil || service.Kinds.Create == "" || service.Kinds.Replace == "" || service.Kinds.Delete == "" ||
		service.States.Pending == "" || service.States.Succeeded == "" || service.States.Failed == "" {
		return errServiceLinkPolicyServiceMisconfigured
	}
	return nil
}

func newServiceLinkOperationID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "service-link-operation"
	}
	return "op_" + hex.EncodeToString(buffer)
}
