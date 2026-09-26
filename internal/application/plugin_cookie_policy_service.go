package application

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

// SnapshotActivationLock serializes mutations that publish a complete runtime snapshot.
var SnapshotActivationLock sync.Mutex

type PluginCookiePolicyService struct {
	Store            interfaces.PluginCookiePolicyStore
	Activator        interfaces.PluginCookiePolicyActivator
	AuditAction      string
	Resource         string
	Success          string
	Invalid          string
	RevisionConflict string
}

func (service *PluginCookiePolicyService) Get(ctx context.Context, instanceID, capability string) (models.PluginCookiePolicy, error) {
	if service == nil || service.Store == nil {
		return models.PluginCookiePolicy{}, models.PluginCookiePolicyUnavailable{}
	}
	return service.Store.Get(ctx, instanceID, capability)
}

func (service *PluginCookiePolicyService) Replace(ctx context.Context, expectedRevision int64, policy models.PluginCookiePolicy, actor, requestID string) (models.PluginCookiePolicy, error) {
	if service == nil || service.Store == nil || service.Activator == nil {
		return models.PluginCookiePolicy{}, models.PluginCookiePolicyUnavailable{Message: policyError(service)}
	}
	if actor == "" || requestID == "" || service.AuditAction == "" || service.Resource == "" || service.Success == "" || service.Invalid == "" {
		return models.PluginCookiePolicy{}, models.PluginCookiePolicyValidationError{Message: policyError(service)}
	}
	SnapshotActivationLock.Lock()
	defer SnapshotActivationLock.Unlock()
	current, err := service.Store.Get(ctx, policy.InstanceID, policy.Capability)
	if err != nil {
		return models.PluginCookiePolicy{}, err
	}
	if current.Revision != expectedRevision {
		return models.PluginCookiePolicy{}, models.PluginCookiePolicyRevisionConflict{Message: service.RevisionConflict}
	}
	rollback, err := service.Activator.ActivatePluginCookiePolicy(ctx, policy)
	if err != nil {
		return models.PluginCookiePolicy{}, err
	}
	committed, err := service.Store.CompareAndSwap(ctx, expectedRevision, policy, models.AuditRecord{
		Timestamp: time.Now().UTC(), Actor: actor, Action: service.AuditAction,
		Resource: service.Resource, Result: service.Success, RequestID: requestID,
	})
	if err != nil && rollback != nil {
		if rollbackErr := rollback(ctx); rollbackErr != nil {
			return models.PluginCookiePolicy{}, errors.Join(err, rollbackErr)
		}
	}
	return committed, err
}

func policyError(service *PluginCookiePolicyService) string {
	if service == nil {
		return ""
	}
	return service.Invalid
}
