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
}

func (store bundleValidationStore) Current(_ context.Context, instanceID string) (models.ServiceConfigurationRevision, models.ServiceConfigurationPointers, error) {
	return models.ServiceConfigurationRevision{InstanceID: instanceID, Revision: 7, SchemaVersion: 1}, models.ServiceConfigurationPointers{PendingRevision: store.pendingRevision}, nil
}

func (store bundleValidationStore) RolloutBlockers(_ context.Context, instanceID string) ([]models.ConfigurationBlocker, error) {
	return append([]models.ConfigurationBlocker(nil), store.blockers[instanceID]...), nil
}

type bundleValidationApplier struct {
	validated []string
	rejected  error
}

func (applier *bundleValidationApplier) ApplyConfiguration(context.Context, string, string, []byte) error {
	panic("rejected bundle must not reach activation")
}

func (applier *bundleValidationApplier) ValidateTrafficRolloutConfiguration(_ context.Context, id string, _ []byte) error {
	applier.validated = append(applier.validated, id)
	if id == "second" {
		return applier.rejected
	}
	return nil
}

func TestBundleValidatesEveryServiceBeforeSubmittingAnyCandidate(t *testing.T) {
	rejected := errors.New("candidate rejected")
	applier := &bundleValidationApplier{rejected: rejected}
	service := ConfigBundleService{Configurations: &ServiceConfigurationService{Store: bundleValidationStore{}, Applier: applier}}
	request := models.ConfigBundleRequest{
		Project: models.ConfigBundleProject{ID: "project", Revision: "revision"},
		Bundle:  models.ConfigBundleManifest{SchemaVersion: "1", Digest: "digest", Services: []models.ConfigBundleService{{ID: "first", Settings: []byte(`{}`)}, {ID: "second", Settings: []byte(`{}`)}}},
		Target:  models.ConfigBundleTarget{Environment: "local"},
	}
	_, err := service.Apply(context.Background(), request, "actor", "request", "key")
	if !errors.Is(err, rejected) || len(applier.validated) != 2 {
		t.Fatalf("bundle was not rejected before submission: validated=%v err=%v", applier.validated, err)
	}
}

func TestServiceRefusalDoesNotCreateOrActivateCandidate(t *testing.T) {
	rejected := errors.New("candidate rejected")
	applier := &bundleValidationApplier{rejected: rejected}
	service := ServiceConfigurationService{Store: bundleValidationStore{}, Applier: applier}
	_, err := service.Apply(context.Background(), ApplyServiceConfigurationCommand{
		InstanceID: "second", ExpectedRevision: 7, SchemaVersion: 1, SettingsJSON: []byte(`{}`),
	})
	if !errors.Is(err, rejected) || len(applier.validated) != 1 {
		t.Fatalf("service refusal did not preserve current configuration: validated=%v err=%v", applier.validated, err)
	}
}

func TestSubmissionBlockersIncludesPendingConfiguration(t *testing.T) {
	service := ServiceConfigurationService{Store: bundleValidationStore{pendingRevision: 8}}
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
	service := ConfigBundleService{Configurations: &ServiceConfigurationService{Store: store, Applier: applier}}
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
