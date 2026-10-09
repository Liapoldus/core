package application

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type TrafficRolloutReplicaSource interface {
	TrafficRolloutReplicas(context.Context, string) ([]models.TrafficRolloutReplica, error)
}

type TrafficRolloutConfigurationValidator interface {
	ValidateTrafficRolloutConfiguration(context.Context, string, []byte) error
}

type TrafficRolloutStates struct {
	Running   string
	Active    string
	Pending   string
	Completed string
}

// ConfigurationCohortHeld prevents generic config propagation from replacing
// the frozen release cohort while an explicit traffic rollout is not complete.
func (service *TrafficRolloutService) ConfigurationCohortHeld(ctx context.Context, instanceID string) (bool, error) {
	if service == nil || service.Store == nil || instanceID == "" || service.States.Completed == "" {
		return false, models.TrafficRolloutInvalid{}
	}
	return service.Store.ConfigurationCohortHeld(ctx, instanceID, service.States.Completed)
}

// ControllerIntents returns only rollouts whose exact candidate configuration
// barrier has closed. Product traffic remains outside Core; this is control
// metadata for the separately authenticated traffic-controller listener.
func (service *TrafficRolloutService) ControllerIntents(ctx context.Context) ([]models.TrafficRolloutRecord, error) {
	if service == nil || service.Store == nil {
		return nil, models.TrafficRolloutInvalid{}
	}
	return service.Store.ControllerVisibleRollouts(ctx)
}

type TrafficRolloutService struct {
	Store                     interfaces.TrafficRolloutStore
	ConfigurationStore        interfaces.PluginConfigurationReader
	ConfigurationRollouts     interfaces.PluginConfigurationRolloutStore
	Applier                   interfaces.PluginConfigurationRolloutApplier
	Operations                OperationService
	Replicas                  TrafficRolloutReplicaSource
	Validator                 TrafficRolloutConfigurationValidator
	States                    TrafficRolloutStates
	SchemaVersion             int64
	MaximumConfigurationBytes int
	OperationStates           PluginConfigurationOperationStates
	TargetLostCode            string
	ScheduleWorker            func(func())
	WorkerContext             context.Context
	Now                       func() time.Time
	workersMu                 sync.Mutex
	workers                   map[string]struct{}
	reconciling               map[string]struct{}
}

type CreateTrafficRolloutCommand struct {
	ID               string
	ExpectedRevision int64
	SchemaVersion    int64
	Configuration    []byte
	Plan             []byte
	Reservation      models.OperationReservation
	Audit            models.AuditRecord
}

func (service *TrafficRolloutService) Create(ctx context.Context, command CreateTrafficRolloutCommand) (models.Operation, bool, error) {
	if service == nil || service.Store == nil || service.Operations.Store == nil || service.Replicas == nil || service.Validator == nil ||
		service.States.Running == "" || service.States.Active == "" || service.States.Pending == "" {
		return models.Operation{}, false, models.TrafficRolloutInvalid{}
	}
	reservation := command.Reservation
	if command.ID == "" || command.ExpectedRevision < 0 || command.SchemaVersion < 1 || len(command.Configuration) == 0 ||
		!json.Valid(command.Configuration) || reservation.Operation.ID == "" || reservation.Operation.Resource == "" ||
		reservation.Payload != nil {
		return models.Operation{}, false, models.TrafficRolloutInvalid{}
	}
	nowProvider := service.Now
	if nowProvider == nil {
		nowProvider = time.Now
	}
	// Replica registration, renewal, deregistration, and generation promotion
	// share this lock. The cohort snapshot therefore cannot race membership changes.
	SnapshotActivationLock.Lock()
	defer SnapshotActivationLock.Unlock()

	operation, found, err := service.Operations.FindByIdempotency(ctx, reservation.Operation.Actor,
		reservation.Scope, reservation.Key, reservation.RequestDigest)
	if err != nil {
		return models.Operation{}, false, err
	}
	if found {
		return operation, false, nil
	}
	plan, err := ParseTrafficRolloutPlan(command.Plan)
	if err != nil {
		return models.Operation{}, false, err
	}
	if err := service.Validator.ValidateTrafficRolloutConfiguration(ctx, reservation.Operation.Resource, command.Configuration); err != nil {
		return models.Operation{}, false, err
	}
	now := nowProvider().UTC()
	replicas, err := service.Replicas.TrafficRolloutReplicas(ctx, reservation.Operation.Resource)
	if err != nil {
		return models.Operation{}, false, err
	}
	candidates, incumbents, err := SelectTrafficRolloutCohorts(plan, replicas, now)
	if err != nil {
		return models.Operation{}, false, err
	}
	record := models.TrafficRolloutRecord{
		ID: command.ID, OperationID: reservation.Operation.ID, InstanceID: reservation.Operation.Resource,
		ReleaseSHA256: plan.ReleaseSHA256, PlanJSON: append([]byte(nil), command.Plan...), State: service.States.Running,
		ActiveStageIndex: 0, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	record.Stages = make([]models.TrafficRolloutStageRecord, len(plan.Stages))
	for index, stage := range plan.Stages {
		state := service.States.Pending
		startedAt := time.Time{}
		if index == 0 {
			state, startedAt = service.States.Active, now
		}
		record.Stages[index] = models.TrafficRolloutStageRecord{Index: index, Stage: stage, State: state, StartedAt: startedAt}
	}
	for _, cohort := range []struct {
		name    string
		targets []models.PluginRolloutTarget
	}{{"candidate", candidates}, {"incumbent", incumbents}} {
		for _, target := range cohort.targets {
			record.Targets = append(record.Targets, models.TrafficRolloutCohortTarget{
				Cohort: cohort.name, ReplicaID: target.ReplicaID, IncarnationID: target.IncarnationID,
				ReleaseSHA256: target.ReleaseSHA256, LeaseExpiresAt: target.LeaseExpiresAt,
			})
		}
	}
	reservation.Operation.State = service.States.Pending
	operation, created, err := service.Store.CreateAndPromote(ctx, models.TrafficRolloutSubmission{
		Reservation: reservation, ExpectedRevision: command.ExpectedRevision, SchemaVersion: command.SchemaVersion,
		SettingsJSON: append([]byte(nil), command.Configuration...), Record: record, Audit: command.Audit,
	})
	if err != nil || !created {
		return operation, created, err
	}
	service.scheduleInstance(reservation.Operation.Resource)
	return operation, true, nil
}

// ReconcileInstance resumes durable candidate-generation Reload barriers for
// one instance. It never discovers replacement incarnations or replays an ACK.
func (service *TrafficRolloutService) ReconcileInstance(ctx context.Context, instanceID string) error {
	if service == nil || service.Store == nil || service.ConfigurationStore == nil || service.ConfigurationRollouts == nil ||
		service.Applier == nil || instanceID == "" {
		return models.TrafficRolloutInvalid{}
	}
	service.workersMu.Lock()
	if service.reconciling == nil {
		service.reconciling = make(map[string]struct{})
	}
	if _, busy := service.reconciling[instanceID]; busy {
		service.workersMu.Unlock()
		return models.PluginConfigurationConvergencePending{}
	}
	service.reconciling[instanceID] = struct{}{}
	service.workersMu.Unlock()
	defer func() {
		service.workersMu.Lock()
		delete(service.reconciling, instanceID)
		service.workersMu.Unlock()
	}()
	rollouts, err := service.Store.OpenConfigurationRollouts(ctx, instanceID)
	if err != nil {
		return err
	}
	var pending error
	for _, rollout := range rollouts {
		if rollout.InstanceID != instanceID {
			return models.TrafficRolloutInvalid{}
		}
		current, _, err := service.ConfigurationStore.Current(ctx, instanceID)
		if err != nil || current.Revision != rollout.Generation {
			pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
			continue
		}
		candidate, err := service.ConfigurationStore.GetRevision(ctx, instanceID, rollout.Generation)
		if err != nil || candidate.Revision != rollout.Generation || candidate.InstanceID != instanceID {
			pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
			continue
		}
		targets, found, err := service.ConfigurationRollouts.Targets(ctx, rollout.OperationID)
		if err != nil || !found || len(targets) == 0 {
			pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
			continue
		}
		lost, err := service.Applier.LostConfigurationTargets(ctx, instanceID, targets)
		if err != nil {
			pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
			continue
		}
		if len(lost) != 0 {
			states := service.OperationStates
			if states.Pending == "" || states.Running == "" || states.Failed == "" || service.TargetLostCode == "" {
				pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
				continue
			}
			if err := service.ConfigurationRollouts.FailRolloutTargetLost(ctx, rollout.OperationID, lost,
				states.Pending, states.Running, states.Failed, service.TargetLostCode); err != nil {
				pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
			}
			continue
		}
		acknowledged, applyErr := service.Applier.ApplyConfigurationToTargets(ctx, rollout.OperationID,
			instanceID, strconv.FormatInt(rollout.Generation, 10), candidate.SettingsJSON, targets)
		if err := service.ConfigurationRollouts.AcknowledgeTargets(ctx, rollout.OperationID, acknowledged); err != nil {
			pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
			continue
		}
		if applyErr != nil {
			pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
			continue
		}
		latest, found, err := service.ConfigurationRollouts.Targets(ctx, rollout.OperationID)
		if err != nil || !found || len(latest) == 0 {
			pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
			continue
		}
		allAcknowledged := true
		for _, target := range latest {
			allAcknowledged = allAcknowledged && target.Acknowledged
		}
		if !allAcknowledged {
			pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
			continue
		}
		if err := service.ConfigurationRollouts.CompleteRollout(ctx, rollout.OperationID); err != nil {
			pending = errors.Join(pending, models.PluginConfigurationConvergencePending{})
		}
	}
	return pending
}

func (service *TrafficRolloutService) scheduleInstance(instanceID string) {
	if service == nil || service.ScheduleWorker == nil || instanceID == "" {
		return
	}
	service.workersMu.Lock()
	if service.workers == nil {
		service.workers = make(map[string]struct{})
	}
	if _, exists := service.workers[instanceID]; exists {
		service.workersMu.Unlock()
		return
	}
	service.workers[instanceID] = struct{}{}
	service.workersMu.Unlock()
	service.ScheduleWorker(func() {
		defer func() {
			service.workersMu.Lock()
			delete(service.workers, instanceID)
			service.workersMu.Unlock()
		}()
		workerContext := service.WorkerContext
		if workerContext == nil {
			workerContext = context.Background()
		}
		_ = service.ReconcileInstance(workerContext, instanceID)
	})
}

func (service *TrafficRolloutService) ApproveStage(ctx context.Context, reservation models.OperationReservation, rolloutID string, revision int64, stageID string, audit models.AuditRecord) (models.Operation, bool, error) {
	if service == nil || service.Store == nil || rolloutID == "" || revision < 1 || stageID == "" || reservation.Operation.ID == "" {
		return models.Operation{}, false, models.TrafficRolloutInvalid{}
	}
	now := time.Now
	if service.Now != nil {
		now = service.Now
	}
	return service.Store.ApproveStageWithOperation(ctx, reservation, rolloutID, revision, stageID, now().UTC(), audit)
}

func (service *TrafficRolloutService) Get(ctx context.Context, id string) (models.TrafficRolloutRecord, error) {
	if service == nil || service.Store == nil || id == "" {
		return models.TrafficRolloutRecord{}, models.TrafficRolloutInvalid{}
	}
	return service.Store.Get(ctx, id)
}
