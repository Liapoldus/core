package application

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type PluginConfigurationService struct {
	Store                 interfaces.PluginConfigurationStore
	Applier               interfaces.PluginConfigurationApplier
	Operations            OperationService
	Unavailable           string
	OperationKind         string
	OperationStates       PluginConfigurationOperationStates
	OperationFailureCodes PluginConfigurationFailureCodes
	RevisionStates        PluginConfigurationRevisionStates
	RecoveryAudit         PluginConfigurationRecoveryAudit
	PayloadVersion        int64
	MaximumPayloadBytes   int
	PayloadFailureCode    string
	ScheduleWorker        func(func())
	operationWorkersMu    sync.Mutex
	operationWorkers      map[string]struct{}
	fencedInstancesMu     sync.RWMutex
	fencedInstances       map[string]struct{}
}

type PluginConfigurationOperationStates struct {
	Pending   string
	Running   string
	Succeeded string
	Failed    string
}

type PluginConfigurationFailureCodes struct {
	Rejected    string
	Conflict    string
	Unavailable string
	ApplyFailed string
}

type PluginConfigurationRevisionStates struct {
	Active    string
	Candidate string
	Failed    string
}

type PluginConfigurationRecoveryAudit struct {
	CandidateAction string
	AppliedAction   string
	FailedAction    string
	Succeeded       string
	Failed          string
}

var SnapshotActivationLock sync.Mutex

type ApplyPluginConfigurationCommand struct {
	OperationID       string
	InstanceID        string
	ExpectedRevision  int64
	CandidateRevision int64
	SchemaVersion     int64
	SettingsJSON      []byte
	CandidateAudit    models.AuditRecord
	AppliedAudit      models.AuditRecord
	FailedAudit       models.AuditRecord
}

func (service *PluginConfigurationService) Submit(ctx context.Context, command ApplyPluginConfigurationCommand, reservation models.OperationReservation) (models.Operation, error) {
	if service == nil {
		return models.Operation{}, models.PluginConfigurationUnavailable{}
	}
	if service.Store == nil || service.Applier == nil || service.Operations.Store == nil {
		return models.Operation{}, models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	if reservation.Operation.ID == "" || reservation.Operation.Kind == "" || reservation.Operation.Actor == "" || reservation.Operation.RequestID == "" || reservation.Scope == "" || reservation.Key == "" || reservation.RequestDigest == "" {
		return models.Operation{}, models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	if reservation.Payload == nil || !reservation.Payload.Valid() || reservation.Payload.Version != service.PayloadVersion ||
		reservation.Payload.Resource != command.InstanceID || reservation.Payload.ExpectedRevision != command.ExpectedRevision ||
		reservation.Payload.SchemaVersion != command.SchemaVersion || len(command.SettingsJSON) > service.MaximumPayloadBytes ||
		reservation.Payload.Digest != reservation.Payload.ComputeDigest(command.SettingsJSON) {
		if len(command.SettingsJSON) > service.MaximumPayloadBytes {
			return models.Operation{}, models.PluginConfigurationPayloadTooLarge{}
		}
		return models.Operation{}, models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	operation, found, err := service.Operations.FindByIdempotency(ctx, reservation.Operation.Actor, reservation.Scope, reservation.Key, reservation.RequestDigest)
	if err != nil {
		return models.Operation{}, err
	}
	if found {
		return operation, nil
	}
	current, _, err := service.Store.Current(ctx, command.InstanceID)
	if err != nil {
		return models.Operation{}, err
	}
	if current.Revision != command.ExpectedRevision {
		return models.Operation{}, models.PluginConfigurationConflict{}
	}
	reservation.Operation.State = service.OperationStates.Pending
	operation, created, err := service.Operations.Reserve(ctx, reservation)
	if err != nil {
		return models.Operation{}, err
	}
	if !created {
		return operation, nil
	}
	candidate, err := service.Store.CreateCandidate(ctx, operation.ID, service.OperationKind, service.OperationStates.Pending,
		command.InstanceID, command.ExpectedRevision, command.SchemaVersion, command.SettingsJSON,
		withConfigurationAudit(command.CandidateAudit, command.InstanceID))
	if err != nil {
		_ = service.Operations.Transition(ctx, operation.ID, service.OperationStates.Pending, service.OperationStates.Failed, service.OperationFailureCodes.Conflict)
		return models.Operation{}, err
	}
	command.CandidateRevision = candidate.Revision
	service.scheduleApply(command, operation)
	return operation, nil
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

func (service *PluginConfigurationService) IsInstanceFenced(instanceID string) bool {
	return service != nil && service.instanceFenced(instanceID)
}

func (service *PluginConfigurationService) Apply(ctx context.Context, command ApplyPluginConfigurationCommand) (models.PluginConfigurationRevision, error) {
	if service == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{}
	}
	if service.Store == nil || service.Applier == nil {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	if command.InstanceID == "" || command.ExpectedRevision < 0 || command.SchemaVersion < 1 {
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
	command.CandidateAudit.DigestBefore = current.Digest
	var candidate models.PluginConfigurationRevision
	if command.CandidateRevision > 0 {
		candidate, err = service.Store.GetRevision(ctx, command.InstanceID, command.CandidateRevision)
	} else {
		candidate, err = service.Store.CreateCandidate(ctx, command.OperationID, service.OperationKind, service.OperationStates.Running,
			command.InstanceID, command.ExpectedRevision, command.SchemaVersion, command.SettingsJSON,
			withConfigurationAudit(command.CandidateAudit, command.InstanceID))
	}
	if err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	if candidate.State != service.RevisionStates.Candidate || candidate.SchemaVersion != command.SchemaVersion {
		return models.PluginConfigurationRevision{}, models.PluginConfigurationConflict{}
	}
	transitionContext := context.WithoutCancel(ctx)
	command.AppliedAudit.DigestBefore = current.Digest
	command.AppliedAudit.DigestAfter = candidate.Digest
	if _, err := service.Store.ActivateCandidate(transitionContext, command.InstanceID, candidate.Revision, command.ExpectedRevision,
		withConfigurationAudit(command.AppliedAudit, command.InstanceID)); err != nil {
		return models.PluginConfigurationRevision{}, err
	}
	if err := service.Applier.ApplyConfiguration(ctx, command.InstanceID, strconv.FormatInt(candidate.Revision, 10), candidate.SettingsJSON); err != nil {
		service.fenceInstance(command.InstanceID)
		return models.PluginConfigurationRevision{}, err
	}
	service.unfenceInstance(command.InstanceID)
	return service.Store.GetRevision(transitionContext, command.InstanceID, candidate.Revision)
}

func (service *PluginConfigurationService) scheduleApply(command ApplyPluginConfigurationCommand, operation models.Operation) {
	if operation.State != service.OperationStates.Pending && operation.State != service.OperationStates.Running {
		return
	}
	service.operationWorkersMu.Lock()
	if service.operationWorkers == nil {
		service.operationWorkers = make(map[string]struct{})
	}
	if _, exists := service.operationWorkers[operation.ID]; exists {
		service.operationWorkersMu.Unlock()
		return
	}
	service.operationWorkers[operation.ID] = struct{}{}
	service.operationWorkersMu.Unlock()
	command.OperationID = operation.ID
	worker := func() {
		defer func() {
			service.operationWorkersMu.Lock()
			delete(service.operationWorkers, operation.ID)
			service.operationWorkersMu.Unlock()
		}()
		workerContext := context.Background()
		if operation.State == service.OperationStates.Pending {
			if err := service.Operations.Transition(workerContext, operation.ID, service.OperationStates.Pending, service.OperationStates.Running, ""); err != nil {
				return
			}
		}
		_, err := service.Apply(workerContext, command)
		if err != nil {
			return
		}
		_ = service.Operations.Transition(workerContext, operation.ID, service.OperationStates.Running, service.OperationStates.Succeeded, "")
	}
	if service.ScheduleWorker != nil {
		service.ScheduleWorker(worker)
		return
	}
	go worker()
}

func (service *PluginConfigurationService) Recover(ctx context.Context) error {
	if service == nil {
		return models.PluginConfigurationUnavailable{}
	}
	if service.Store == nil || service.Applier == nil || service.Operations.Store == nil || service.OperationKind == "" {
		return models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	SnapshotActivationLock.Lock()
	defer SnapshotActivationLock.Unlock()
	operations, err := service.Operations.ListRecoverable(ctx, service.OperationKind, service.OperationStates.Pending, service.OperationStates.Running)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if err := service.recoverOperation(ctx, operation); err != nil {
			return err
		}
	}
	return nil
}

func (service *PluginConfigurationService) recoverOperation(ctx context.Context, operation models.Operation) error {
	current, pointers, err := service.Store.Current(ctx, operation.Resource)
	if err != nil {
		return err
	}
	if operation.CandidateRevision < 1 {
		if pointers.PendingRevision != 0 {
			service.fenceInstance(operation.Resource)
			return nil
		}
		payload, found, payloadErr := service.Operations.Store.Payload(ctx, operation.ID)
		if payloadErr != nil {
			return payloadErr
		}
		if !found || !payload.Valid() || payload.Version != service.PayloadVersion || payload.Resource != operation.Resource ||
			payload.ExpectedRevision < 0 || payload.SchemaVersion < 1 {
			return service.failCorruptReservation(ctx, operation)
		}
		return service.failCorruptReservation(ctx, operation)
	}
	candidate, err := service.Store.GetRevision(ctx, operation.Resource, operation.CandidateRevision)
	if err != nil {
		return err
	}
	if current.Revision == candidate.Revision && candidate.State == service.RevisionStates.Active {
		if err := service.Applier.ApplyConfiguration(ctx, operation.Resource, strconv.FormatInt(candidate.Revision, 10), candidate.SettingsJSON); err != nil {
			service.fenceInstance(operation.Resource)
			return nil
		}
		service.unfenceInstance(operation.Resource)
		return service.Operations.Transition(ctx, operation.ID, operation.State, service.OperationStates.Succeeded, "")
	}
	if candidate.State == service.RevisionStates.Failed && pointers.PendingRevision == 0 {
		return service.Operations.Transition(ctx, operation.ID, operation.State, service.OperationStates.Failed, service.OperationFailureCodes.ApplyFailed)
	}
	if candidate.State != service.RevisionStates.Candidate || pointers.PendingRevision != candidate.Revision || current.Revision >= candidate.Revision {
		service.fenceInstance(operation.Resource)
		return nil
	}
	payload, found, err := service.Operations.Store.Payload(ctx, operation.ID)
	if err != nil {
		return err
	}
	if !found || !payload.Valid() || payload.Version != service.PayloadVersion || payload.Resource != operation.Resource ||
		payload.ExpectedRevision != current.Revision || payload.SchemaVersion != candidate.SchemaVersion ||
		payload.Digest != payload.ComputeDigest(candidate.SettingsJSON) {
		failedAudit := models.AuditRecord{Actor: operation.Actor, Action: service.RecoveryAudit.FailedAction, Result: service.RecoveryAudit.Failed, RequestID: operation.RequestID}
		if _, failErr := service.Store.FailCandidate(ctx, operation.Resource, candidate.Revision, current.Revision,
			withConfigurationAudit(failedAudit, operation.Resource)); failErr != nil {
			return failErr
		}
		return service.Operations.Transition(ctx, operation.ID, operation.State, service.OperationStates.Failed, service.PayloadFailureCode)
	}
	appliedAudit := models.AuditRecord{
		Actor: operation.Actor, Action: service.RecoveryAudit.AppliedAction,
		Result: service.RecoveryAudit.Succeeded, RequestID: operation.RequestID,
	}
	if _, err := service.Store.ActivateCandidate(ctx, operation.Resource, candidate.Revision, current.Revision,
		withConfigurationAudit(appliedAudit, operation.Resource)); err != nil {
		return err
	}
	if err := service.Applier.ApplyConfiguration(ctx, operation.Resource, strconv.FormatInt(candidate.Revision, 10), candidate.SettingsJSON); err != nil {
		service.fenceInstance(operation.Resource)
		return nil
	}
	service.unfenceInstance(operation.Resource)
	return service.Operations.Transition(ctx, operation.ID, operation.State, service.OperationStates.Succeeded, "")
}

func (service *PluginConfigurationService) failCorruptReservation(ctx context.Context, operation models.Operation) error {
	return service.Operations.Transition(ctx, operation.ID, operation.State, service.OperationStates.Failed, service.PayloadFailureCode)
}

func (service *PluginConfigurationService) fenceInstance(instanceID string) {
	service.fencedInstancesMu.Lock()
	defer service.fencedInstancesMu.Unlock()
	if service.fencedInstances == nil {
		service.fencedInstances = make(map[string]struct{})
	}
	service.fencedInstances[instanceID] = struct{}{}
}

func (service *PluginConfigurationService) instanceFenced(instanceID string) bool {
	service.fencedInstancesMu.RLock()
	defer service.fencedInstancesMu.RUnlock()
	_, fenced := service.fencedInstances[instanceID]
	return fenced
}

func (service *PluginConfigurationService) unfenceInstance(instanceID string) {
	service.fencedInstancesMu.Lock()
	defer service.fencedInstancesMu.Unlock()
	delete(service.fencedInstances, instanceID)
}

func withConfigurationAudit(audit models.AuditRecord, instanceID string) models.AuditRecord {
	audit.Timestamp = time.Now().UTC()
	audit.Resource = instanceID
	return audit
}
