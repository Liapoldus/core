package application

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type PluginConfigurationService struct {
	Store                 interfaces.PluginConfigurationStore
	Applier               interfaces.PluginConfigurationApplier
	Operations            OperationService
	Unavailable           string
	OperationKind         string
	RollbackOperationKind string
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
	TargetLost  string
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

// RestorePreviousConfiguration swaps active and previous. Registered v2
// instances commit the exact authenticated replica cohort with that pointer
// swap; fixed-endpoint instances retain the ordinary store path.
func (service *PluginConfigurationService) RestorePreviousConfiguration(
	ctx context.Context,
	operationID, instanceID string,
	expectedCurrent int64,
	audit models.AuditRecord,
) (models.PluginConfigurationRevision, bool, error) {
	if service == nil || service.Store == nil || service.Applier == nil || operationID == "" || instanceID == "" {
		return models.PluginConfigurationRevision{}, false, models.PluginConfigurationUnavailable{}
	}
	current, pointers, err := service.Store.Current(ctx, instanceID)
	if err != nil {
		return models.PluginConfigurationRevision{}, false, err
	}
	if current.Revision != expectedCurrent || pointers.PreviousRevision < 1 {
		return models.PluginConfigurationRevision{}, false, models.PluginConfigurationConflict{}
	}
	previous, err := service.Store.GetRevision(ctx, instanceID, pointers.PreviousRevision)
	if err != nil {
		return models.PluginConfigurationRevision{}, false, err
	}
	if rolloutApplier, ok := service.Applier.(interfaces.PluginConfigurationRolloutApplier); ok {
		targets, registered, err := rolloutApplier.CaptureConfigurationTargets(ctx, instanceID)
		if err != nil {
			return models.PluginConfigurationRevision{}, false, err
		}
		if registered {
			rolloutStore, supported := service.Store.(interfaces.PluginConfigurationRolloutStore)
			if !supported || service.RollbackOperationKind == "" {
				return models.PluginConfigurationRevision{}, false, models.PluginConfigurationUnavailable{Message: service.Unavailable}
			}
			_, err = rolloutStore.RestorePreviousWithTargets(ctx, operationID, service.RollbackOperationKind,
				instanceID, expectedCurrent, withConfigurationAudit(audit, instanceID), targets)
			if err != nil {
				return models.PluginConfigurationRevision{}, true, err
			}
			previous.State = service.RevisionStates.Active
			return previous, true, nil
		}
	}
	_, err = service.Store.RestorePrevious(ctx, instanceID, expectedCurrent, withConfigurationAudit(audit, instanceID))
	if err != nil {
		return models.PluginConfigurationRevision{}, false, err
	}
	previous.State = service.RevisionStates.Active
	return previous, false, nil
}

// ApplyRestoredConfiguration applies a generation already made active by a
// rollback. Registered instances use their persisted exact-target rollout.
func (service *PluginConfigurationService) ApplyRestoredConfiguration(ctx context.Context, operationID string, revision models.PluginConfigurationRevision) error {
	if service == nil || service.Store == nil || service.Applier == nil || operationID == "" || revision.InstanceID == "" || revision.Revision < 1 {
		return models.PluginConfigurationUnavailable{}
	}
	return service.applyCandidate(ctx, operationID, revision)
}

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
	if _, err := service.activateCandidate(transitionContext, command, candidate); err != nil {
		if _, failErr := service.Store.FailCandidate(transitionContext, command.InstanceID, candidate.Revision,
			command.ExpectedRevision, withConfigurationAudit(command.FailedAudit, command.InstanceID)); failErr != nil {
			return models.PluginConfigurationRevision{}, errors.Join(err, failErr)
		}
		return models.PluginConfigurationRevision{}, err
	}
	if err := service.applyCandidate(ctx, command.OperationID, candidate); err != nil {
		var convergencePending models.PluginConfigurationConvergencePending
		if !errors.As(err, &convergencePending) {
			if closeErr := service.closeConfigurationRollout(transitionContext, command.OperationID); closeErr != nil {
				return models.PluginConfigurationRevision{}, errors.Join(err, closeErr)
			}
		}
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
			var convergencePending models.PluginConfigurationConvergencePending
			if errors.As(err, &convergencePending) {
				return
			}
			code := service.OperationFailureCodes.ApplyFailed
			var conflict models.PluginConfigurationConflict
			if errors.As(err, &conflict) {
				code = service.OperationFailureCodes.Conflict
			}
			_ = service.Operations.Transition(workerContext, operation.ID,
				service.OperationStates.Running, service.OperationStates.Failed, code)
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
	return service.recover(ctx, nil)
}

// RecoverRegistered retries only durable configuration operations whose
// instance has participated in the authenticated replica directory. This keeps
// the v2 background roll-forward isolated from static v1 plugin endpoints.
func (service *PluginConfigurationService) RecoverRegistered(ctx context.Context, hasRegistered func(string) bool) error {
	if service == nil {
		return models.PluginConfigurationUnavailable{}
	}
	if hasRegistered == nil {
		return models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	return service.recover(ctx, hasRegistered)
}

func (service *PluginConfigurationService) recover(ctx context.Context, hasRegistered func(string) bool) error {
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
		if hasRegistered != nil && !hasRegistered(operation.Resource) {
			continue
		}
		if err := service.recoverOperation(ctx, operation); err != nil {
			return err
		}
	}
	if hasRegistered != nil && service.RollbackOperationKind != "" {
		if err := service.recoverRegisteredRollbacks(ctx, hasRegistered); err != nil {
			return err
		}
	}
	return nil
}

func (service *PluginConfigurationService) recoverRegisteredRollbacks(ctx context.Context, hasRegistered func(string) bool) error {
	rollouts, supported := service.Store.(interfaces.PluginConfigurationRolloutStore)
	if !supported {
		return models.PluginConfigurationUnavailable{Message: service.Unavailable}
	}
	operations, err := service.Operations.ListRecoverable(ctx, service.RollbackOperationKind,
		service.OperationStates.Pending, service.OperationStates.Running)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if !hasRegistered(operation.Resource) {
			continue
		}
		_, found, err := rollouts.Targets(ctx, operation.ID)
		if err != nil {
			return err
		}
		if !found {
			service.fenceInstance(operation.Resource)
			continue
		}
		active, _, err := service.Store.Current(ctx, operation.Resource)
		if err != nil {
			service.fenceInstance(operation.Resource)
			return err
		}
		state := operation.State
		if state == service.OperationStates.Pending {
			if err := service.Operations.Transition(ctx, operation.ID, state, service.OperationStates.Running, ""); err != nil {
				return err
			}
			state = service.OperationStates.Running
		}
		if err := service.applyCandidate(ctx, operation.ID, active); err != nil {
			var pending models.PluginConfigurationConvergencePending
			if errors.As(err, &pending) {
				service.fenceInstance(operation.Resource)
				continue
			}
			service.fenceInstance(operation.Resource)
			return err
		}
		if err := service.Operations.Transition(ctx, operation.ID, state, service.OperationStates.Succeeded, ""); err != nil {
			return err
		}
		service.unfenceInstance(operation.Resource)
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
		if err := service.applyCandidate(ctx, operation.ID, candidate); err != nil {
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
	command := ApplyPluginConfigurationCommand{OperationID: operation.ID, InstanceID: operation.Resource,
		ExpectedRevision: current.Revision, CandidateRevision: candidate.Revision, SchemaVersion: candidate.SchemaVersion,
		SettingsJSON: candidate.SettingsJSON, AppliedAudit: withConfigurationAudit(appliedAudit, operation.Resource),
		FailedAudit: models.AuditRecord{Actor: operation.Actor, Action: service.RecoveryAudit.FailedAction,
			Result: service.RecoveryAudit.Failed, RequestID: operation.RequestID}}
	if _, err := service.activateCandidate(ctx, command, candidate); err != nil {
		var conflict models.PluginConfigurationConflict
		if errors.As(err, &conflict) {
			if _, failErr := service.Store.FailCandidate(ctx, operation.Resource, candidate.Revision, current.Revision,
				withConfigurationAudit(command.FailedAudit, operation.Resource)); failErr != nil {
				return errors.Join(err, failErr)
			}
			return service.Operations.Transition(ctx, operation.ID, operation.State,
				service.OperationStates.Failed, service.OperationFailureCodes.Conflict)
		}
		return err
	}
	if err := service.applyCandidate(ctx, operation.ID, candidate); err != nil {
		service.fenceInstance(operation.Resource)
		return nil
	}
	service.unfenceInstance(operation.Resource)
	return service.Operations.Transition(ctx, operation.ID, operation.State, service.OperationStates.Succeeded, "")
}

func (service *PluginConfigurationService) activateCandidate(ctx context.Context, command ApplyPluginConfigurationCommand, candidate models.PluginConfigurationRevision) (models.PluginConfigurationPointers, error) {
	if rolloutApplier, ok := service.Applier.(interfaces.PluginConfigurationRolloutApplier); ok {
		targets, registered, err := rolloutApplier.CaptureConfigurationTargets(ctx, command.InstanceID)
		if err != nil {
			return models.PluginConfigurationPointers{}, err
		}
		if registered {
			rolloutStore, supported := service.Store.(interfaces.PluginConfigurationRolloutStore)
			if !supported || command.OperationID == "" {
				return models.PluginConfigurationPointers{}, models.PluginConfigurationUnavailable{Message: service.Unavailable}
			}
			return rolloutStore.ActivateCandidateWithTargets(ctx, command.OperationID, command.InstanceID,
				candidate.Revision, command.ExpectedRevision,
				withConfigurationAudit(command.AppliedAudit, command.InstanceID), targets)
		}
	}
	return service.Store.ActivateCandidate(ctx, command.InstanceID, candidate.Revision, command.ExpectedRevision,
		withConfigurationAudit(command.AppliedAudit, command.InstanceID))
}

func (service *PluginConfigurationService) applyCandidate(ctx context.Context, operationID string, candidate models.PluginConfigurationRevision) error {
	if operationID != "" {
		if rollouts, ok := service.Store.(interfaces.PluginConfigurationRolloutStore); ok {
			targets, found, err := rollouts.Targets(ctx, operationID)
			if err != nil {
				return models.PluginConfigurationConvergencePending{}
			}
			if found {
				applier, supported := service.Applier.(interfaces.PluginConfigurationRolloutApplier)
				if !supported || len(targets) == 0 {
					return models.PluginConfigurationConvergencePending{}
				}
				lost, lossErr := applier.LostConfigurationTargets(ctx, candidate.InstanceID, targets)
				if lossErr != nil {
					return models.PluginConfigurationConvergencePending{}
				}
				if len(lost) != 0 {
					if service.OperationFailureCodes.TargetLost == "" || service.OperationStates.Pending == "" ||
						service.OperationStates.Running == "" || service.OperationStates.Failed == "" {
						return models.PluginConfigurationConvergencePending{}
					}
					if err := rollouts.FailRolloutTargetLost(ctx, operationID, lost,
						service.OperationStates.Pending, service.OperationStates.Running,
						service.OperationStates.Failed, service.OperationFailureCodes.TargetLost); err != nil {
						return models.PluginConfigurationConvergencePending{}
					}
					return models.PluginConfigurationConvergencePending{}
				}
				acknowledged, applyErr := applier.ApplyConfigurationToTargets(ctx, operationID,
					candidate.InstanceID, strconv.FormatInt(candidate.Revision, 10), candidate.SettingsJSON, targets)
				if err := rollouts.AcknowledgeTargets(ctx, operationID, acknowledged); err != nil {
					return models.PluginConfigurationConvergencePending{}
				}
				if applyErr != nil {
					var convergencePending models.PluginConfigurationConvergencePending
					if errors.As(applyErr, &convergencePending) {
						return convergencePending
					}
					return applyErr
				}
				latest, _, err := rollouts.Targets(ctx, operationID)
				if err != nil || len(latest) == 0 {
					return models.PluginConfigurationConvergencePending{}
				}
				for _, target := range latest {
					if !target.Acknowledged {
						return models.PluginConfigurationConvergencePending{}
					}
				}
				if err := rollouts.CompleteRollout(ctx, operationID); err != nil {
					return models.PluginConfigurationConvergencePending{}
				}
				return nil
			}
		}
	}
	return service.Applier.ApplyConfiguration(ctx, candidate.InstanceID, strconv.FormatInt(candidate.Revision, 10), candidate.SettingsJSON)
}

func (service *PluginConfigurationService) failCorruptReservation(ctx context.Context, operation models.Operation) error {
	return service.Operations.Transition(ctx, operation.ID, operation.State, service.OperationStates.Failed, service.PayloadFailureCode)
}

func (service *PluginConfigurationService) closeConfigurationRollout(ctx context.Context, operationID string) error {
	if service == nil || service.Store == nil || operationID == "" {
		return nil
	}
	rollouts, ok := service.Store.(interfaces.PluginConfigurationRolloutStore)
	if !ok {
		return nil
	}
	_, found, err := rollouts.Targets(ctx, operationID)
	if err != nil || !found {
		return err
	}
	return rollouts.CloseRollout(ctx, operationID)
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
