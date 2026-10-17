package application

import (
	"context"
	"errors"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/interfaces"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

type bundleValidationStore struct {
	interfaces.ServiceConfigurationStore
}

func (bundleValidationStore) Current(context.Context, string) (models.ServiceConfigurationRevision, models.ServiceConfigurationPointers, error) {
	return models.ServiceConfigurationRevision{Revision: 7, SchemaVersion: 1}, models.ServiceConfigurationPointers{}, nil
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
