package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type bundleValidationStore struct {
	interfaces.ServiceConfigurationStore
	blockers        map[string][]models.ConfigurationBlocker
	pendingRevision int64
	candidates      map[string]models.ServiceConfigurationRevision
	created         bool
	failed          bool
	activated       bool
}

func (store *bundleValidationStore) Current(_ context.Context, instanceID string) (models.ServiceConfigurationRevision, models.ServiceConfigurationPointers, error) {
	pending := store.pendingRevision
	if candidate, exists := store.candidates[instanceID]; exists {
		pending = candidate.Revision
	}
	return models.ServiceConfigurationRevision{InstanceID: instanceID, Revision: 7, SchemaVersion: 1, Digest: "active-digest"}, models.ServiceConfigurationPointers{CurrentRevision: 7, PendingRevision: pending}, nil
}

func (store *bundleValidationStore) CreateCandidate(_ context.Context, _, _, state, instanceID string, expected, schema int64, raw []byte, _ models.AuditRecord) (models.ServiceConfigurationRevision, error) {
	store.created = true
	if store.candidates == nil {
		store.candidates = make(map[string]models.ServiceConfigurationRevision)
	}
	candidate := models.ServiceConfigurationRevision{InstanceID: instanceID, Revision: expected + 1, SchemaVersion: schema,
		Digest: "candidate-digest", SettingsJSON: append([]byte(nil), raw...), State: "staging"}
	_ = state
	store.candidates[instanceID] = candidate
	return candidate, nil
}

func (store *bundleValidationStore) GetRevision(_ context.Context, instanceID string, revision int64) (models.ServiceConfigurationRevision, error) {
	candidate, exists := store.candidates[instanceID]
	if !exists || candidate.Revision != revision {
		return models.ServiceConfigurationRevision{}, models.ServiceConfigurationNotFound{}
	}
	return candidate, nil
}

func (store *bundleValidationStore) FailCandidate(_ context.Context, instanceID string, revision, expected int64, _ models.AuditRecord) (models.ServiceConfigurationPointers, error) {
	candidate, exists := store.candidates[instanceID]
	if !exists || candidate.Revision != revision || expected != 7 {
		return models.ServiceConfigurationPointers{}, models.ServiceConfigurationConflict{}
	}
	store.failed = true
	delete(store.candidates, instanceID)
	return models.ServiceConfigurationPointers{CurrentRevision: 7}, nil
}

func (store *bundleValidationStore) ActivateCandidate(context.Context, string, int64, int64, models.AuditRecord) (models.ServiceConfigurationPointers, error) {
	store.activated = true
	return models.ServiceConfigurationPointers{}, nil
}

func (store bundleValidationStore) RolloutBlockers(_ context.Context, instanceID string) ([]models.ConfigurationBlocker, error) {
	return append([]models.ConfigurationBlocker(nil), store.blockers[instanceID]...), nil
}

type bundleValidationApplier struct {
	validated   []string
	generations []string
	rejected    error
}

type bundleOperationStore struct {
	interfaces.OperationStore
	operations map[string]models.Operation
	keys       map[string]string
	created    int
	failed     int
}

func bundleOperationKey(actor, scope, key, digest string) string {
	return actor + "\x00" + scope + "\x00" + key + "\x00" + digest
}

func (store *bundleOperationStore) FindByIdempotency(_ context.Context, actor, scope, key, digest string) (models.Operation, bool, error) {
	id, exists := store.keys[bundleOperationKey(actor, scope, key, digest)]
	if !exists {
		return models.Operation{}, false, nil
	}
	operation, exists := store.operations[id]
	return operation, exists, nil
}

func (store *bundleOperationStore) Reserve(_ context.Context, reservation models.OperationReservation) (models.Operation, bool, error) {
	if store.operations == nil {
		store.operations = make(map[string]models.Operation)
		store.keys = make(map[string]string)
	}
	operation := reservation.Operation
	operation.State = "pending"
	store.operations[operation.ID] = operation
	store.keys[bundleOperationKey(operation.Actor, reservation.Scope, reservation.Key, reservation.RequestDigest)] = operation.ID
	store.created++
	return operation, true, nil
}

func (store *bundleOperationStore) Transition(_ context.Context, id, fromState, toState, errorCode string) error {
	operation := store.operations[id]
	if operation.ID == "" || operation.State != fromState {
		return errors.New("unexpected operation transition")
	}
	operation.State, operation.ErrorCode = toState, errorCode
	store.operations[id] = operation
	if toState == "failed" {
		store.failed++
	}
	return nil
}

func (applier *bundleValidationApplier) ApplyConfiguration(context.Context, string, string, []byte) error {
	panic("rejected bundle must not reach activation")
}

func (applier *bundleValidationApplier) ValidateTrafficRolloutConfiguration(_ context.Context, id, generation string, _ []byte) error {
	applier.validated = append(applier.validated, id)
	applier.generations = append(applier.generations, generation)
	if id == "second" {
		return applier.rejected
	}
	return nil
}

func TestBundleStagesAndValidatesEveryServiceBeforeSchedulingAnyCandidate(t *testing.T) {
	rejected := errors.New("candidate rejected")
	applier := &bundleValidationApplier{rejected: rejected}
	store := &bundleValidationStore{}
	operationStore := &bundleOperationStore{}
	scheduled := 0
	configurationService := &ServiceConfigurationService{
		Store: store, Applier: applier, Operations: OperationService{Store: operationStore}, OperationKind: "settings",
		OperationStates:       ServiceConfigurationOperationStates{Pending: "pending", Running: "running", Failed: "failed", Succeeded: "succeeded"},
		OperationFailureCodes: ServiceConfigurationFailureCodes{Rejected: "rejected", Conflict: "conflict", ApplyFailed: "apply_failed"},
		RevisionStates:        ServiceConfigurationRevisionStates{Candidate: "staging", Failed: "failed"},
		PayloadVersion:        1, MaximumPayloadBytes: 1024, ScheduleWorker: func(func()) { scheduled++ },
	}
	service := ConfigBundleService{Configurations: configurationService, Operations: OperationService{Store: operationStore}, OperationKind: "settings",
		Pending: "pending", CandidateAction: "candidate", ApplyAction: "apply", FailedAction: "failed",
		SucceededResult: "succeeded", FailedResult: "failed"}
	request := models.ConfigBundleRequest{
		Project: models.ConfigBundleProject{ID: "project", Revision: "revision"},
		Bundle:  models.ConfigBundleManifest{SchemaVersion: "1", Digest: "digest", Services: []models.ConfigBundleService{{ID: "first", Settings: []byte(`{}`)}, {ID: "second", Settings: []byte(`{}`)}}},
		Target:  models.ConfigBundleTarget{Environment: "local"},
	}
	_, err := service.Apply(context.Background(), request, "actor", "request", "key")
	if !errors.Is(err, rejected) || len(applier.validated) != 2 || applier.generations[0] != "8" || applier.generations[1] != "8" ||
		len(store.candidates) != 0 || !store.failed || operationStore.created != 2 || operationStore.failed != 2 || scheduled != 0 {
		t.Fatalf("bundle preflight was not staged and rolled back atomically: validated=%v generations=%v candidates=%v failed=%v operations=%d/%d scheduled=%d err=%v",
			applier.validated, applier.generations, store.candidates, store.failed, operationStore.created, operationStore.failed, scheduled, err)
	}
}

func TestServiceRefusalFailsStagedCandidateWithoutActivation(t *testing.T) {
	rejected := errors.New("candidate rejected")
	applier := &bundleValidationApplier{rejected: rejected}
	store := &bundleValidationStore{}
	service := ServiceConfigurationService{Store: store, Applier: applier,
		RevisionStates: ServiceConfigurationRevisionStates{Active: "active", Candidate: "staging", Failed: "failed"}}
	_, err := service.Apply(context.Background(), ApplyServiceConfigurationCommand{
		InstanceID: "second", ExpectedRevision: 7, SchemaVersion: 1, SettingsJSON: []byte(`{}`),
	})
	if !errors.Is(err, rejected) || len(applier.validated) != 1 || applier.generations[0] != "8" || !store.created || !store.failed || store.activated {
		t.Fatalf("service refusal did not fail staged candidate before activation: validated=%v created=%v failed=%v activated=%v err=%v", applier.validated, store.created, store.failed, store.activated, err)
	}
}

func TestCandidateValidationFailureUsesConfigurationInvalidOperationCode(t *testing.T) {
	store := &bundleValidationStore{candidates: map[string]models.ServiceConfigurationRevision{
		"second": {InstanceID: "second", Revision: 8, SchemaVersion: 1, SettingsJSON: []byte(`{"auth":true}`), State: "staging"},
	}}
	operationStore := &bundleOperationStore{operations: map[string]models.Operation{
		"operation": {ID: "operation", State: "pending"},
	}}
	applier := &bundleValidationApplier{rejected: models.ServiceConfigurationCandidateValidationFailed{}}
	service := &ServiceConfigurationService{
		Store: store, Applier: applier, Operations: OperationService{Store: operationStore},
		OperationStates:       ServiceConfigurationOperationStates{Pending: "pending", Running: "running", Failed: "failed", Succeeded: "succeeded"},
		OperationFailureCodes: ServiceConfigurationFailureCodes{Rejected: "service_config_invalid", Conflict: "service_revision_conflict", ApplyFailed: "activation_failed"},
		RevisionStates:        ServiceConfigurationRevisionStates{Active: "active", Candidate: "staging", Failed: "failed"},
		ScheduleWorker:        func(run func()) { run() },
	}
	service.scheduleApply(ApplyServiceConfigurationCommand{
		OperationID: "operation", InstanceID: "second", ExpectedRevision: 7, CandidateRevision: 8,
		SchemaVersion: 1, SettingsJSON: []byte(`{"auth":true}`),
	}, models.Operation{ID: "operation", State: "pending"})
	operation := operationStore.operations["operation"]
	if operation.State != "failed" || operation.ErrorCode != "service_config_invalid" || !store.failed || store.activated {
		t.Fatalf("candidate rejection was not reported safely: operation=%+v failed=%v activated=%v", operation, store.failed, store.activated)
	}
}

func TestSubmissionBlockersIncludesPendingConfiguration(t *testing.T) {
	service := ServiceConfigurationService{Store: &bundleValidationStore{pendingRevision: 8}}
	blockers, err := service.SubmissionBlockers(context.Background(), "server")
	if err != nil || len(blockers) != 1 || blockers[0] != models.ConfigurationBlockerPending {
		t.Fatalf("pending configuration blocker = %#v, %v", blockers, err)
	}
}

func TestBundlePlanReportsSafeSubmissionBlockersAndApplyStopsBeforeValidation(t *testing.T) {
	store := bundleValidationStore{blockers: map[string][]models.ConfigurationBlocker{
		"second": {models.ConfigurationBlockerServiceRollout, models.ConfigurationBlockerTrafficRollout},
	}}
	applier := &bundleValidationApplier{}
	service := ConfigBundleService{Configurations: &ServiceConfigurationService{Store: &store, Applier: applier}}
	request := models.ConfigBundleRequest{
		Project: models.ConfigBundleProject{ID: "project", Revision: "revision"},
		Bundle:  models.ConfigBundleManifest{SchemaVersion: "1", Digest: "digest", Services: []models.ConfigBundleService{{ID: "first", Settings: []byte(`{}`)}, {ID: "second", Settings: []byte(`{}`)}}},
		Target:  models.ConfigBundleTarget{Environment: "local"},
	}
	plan, err := service.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"blockedBy":["configuration_rollout_open","traffic_rollout_incomplete"]`) {
		t.Fatalf("plan did not return stable blocker codes: %s", body)
	}
	_, err = service.Apply(context.Background(), request, "actor", "request", "key")
	var conflict models.ServiceConfigurationConflict
	if !errors.As(err, &conflict) || conflict.Reason != string(models.ConfigurationBlockerServiceRollout) {
		t.Fatalf("apply did not fail with the first safe blocker: %#v %v", conflict, err)
	}
	if len(applier.validated) != 0 {
		t.Fatalf("blocked bundle reached service validation: %v", applier.validated)
	}
}
