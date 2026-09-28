package application

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type PluginConfigurationService struct {
	Store       interfaces.PluginConfigurationStore
	Applier     interfaces.PluginConfigurationApplier
	Unavailable string
}

type ApplyPluginConfigurationCommand struct {
	InstanceID       string
	ExpectedRevision int64
	SchemaVersion    int64
	SettingsJSON     []byte
	CandidateAudit   models.AuditRecord
	AppliedAudit     models.AuditRecord
	FailedAudit      models.AuditRecord
}

func (service *PluginConfigurationService) Current(ctx context.Context, instanceID string) (models.PluginConfigurationRevision, error) {
	if service == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{}
	}
	if service.Store == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	revision, _, err := service.Store.Current(ctx, instanceID)
	return revision, err
}

func (service *PluginConfigurationService) Apply(ctx context.Context, command ApplyPluginConfigurationCommand) (models.PluginConfigurationRevision, error) {
	if service == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{}
	}
	if service.Store == nil || service.Applier == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	if command.InstanceID == "" || command.ExpectedRevision < 1 || command.SchemaVersion < 1 || len(command.SettingsJSON) == 0 {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	SnapshotActivationLock.Lock()
	defer SnapshotActivationLock.Unlock()

	current, _, err := service.Store.Current(ctx, command.InstanceID)
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	if current.Revision != command.ExpectedRevision {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationConflict{}
	}
	candidate, err := service.Store.CreateCandidate(ctx, command.InstanceID, command.ExpectedRevision, command.SchemaVersion, command.SettingsJSON, withConfigurationAudit(command.CandidateAudit, command.InstanceID))
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	transitionContext := context.WithoutCancel(ctx)
	if err := service.Applier.ApplyConfiguration(ctx, command.InstanceID, strconv.FormatInt(candidate.Revision, 10), candidate.SettingsJSON); err != nil {
		_, failErr := service.Store.FailCandidate(transitionContext, command.InstanceID, candidate.Revision, command.ExpectedRevision, withConfigurationAudit(command.FailedAudit, command.InstanceID))
		return models.PluginConfigurationRevision{}, errors.Join(err, failErr)
	}
	_, err = service.Store.ActivateCandidate(transitionContext, command.InstanceID, candidate.Revision, command.ExpectedRevision, withConfigurationAudit(command.AppliedAudit, command.InstanceID))
	if err != nil {
		rollbackErr := service.Applier.ApplyConfiguration(transitionContext, command.InstanceID, strconv.FormatInt(current.Revision, 10), current.SettingsJSON)
		return models.PluginConfigurationRevision{}, errors.Join(err, rollbackErr)
	}
	return service.Store.GetRevision(transitionContext, command.InstanceID, candidate.Revision)
}

func withConfigurationAudit(audit models.AuditRecord, instanceID string) models.AuditRecord {
	audit.Timestamp = time.Now().UTC()
	audit.Resource = instanceID
	return audit
}
