package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// ConfigBundleService is the Core-side adapter for the standalone CLI bundle
// contract. It deliberately delegates persistence and rollout to the existing
// service configuration service.
type ConfigBundleService struct {
	Configurations  *ServiceConfigurationService
	Operations      OperationService
	OperationKind   string
	Pending         string
	CandidateAction string
	ApplyAction     string
	FailedAction    string
	SucceededResult string
	FailedResult    string
}

func (service *ConfigBundleService) Plan(ctx context.Context, request models.ConfigBundleRequest) (models.ConfigBundlePlan, error) {
	if err := request.Validate(); err != nil {
		return models.ConfigBundlePlan{}, err
	}
	if service == nil || service.Configurations == nil {
		return models.ConfigBundlePlan{}, errors.New("configuration service unavailable")
	}
	plan := models.ConfigBundlePlan{Valid: true, Digest: request.Bundle.Digest, Services: make([]models.ConfigBundlePlanItem, 0, len(request.Bundle.Services))}
	for _, item := range request.Bundle.Services {
		current, err := service.Configurations.Current(ctx, item.ID)
		if err != nil {
			return models.ConfigBundlePlan{}, fmt.Errorf("service %s: %w", item.ID, err)
		}
		blockers, err := service.Configurations.SubmissionBlockers(ctx, item.ID)
		if err != nil {
			return models.ConfigBundlePlan{}, fmt.Errorf("service %s submission state: %w", item.ID, err)
		}
		plan.Services = append(plan.Services, models.ConfigBundlePlanItem{
			ID: item.ID, CurrentRevision: current.Revision, CurrentDigest: current.Digest,
			SchemaVersion: current.SchemaVersion, BlockedBy: blockers,
		})
	}
	return plan, nil
}

func (service *ConfigBundleService) Apply(ctx context.Context, request models.ConfigBundleRequest, actor, requestID, idempotencyKey string) (models.ConfigBundleApply, error) {
	plan, err := service.Plan(ctx, request)
	if err != nil {
		return models.ConfigBundleApply{}, err
	}
	for _, item := range plan.Services {
		if len(item.BlockedBy) > 0 {
			return models.ConfigBundleApply{}, models.ServiceConfigurationConflict{Reason: string(item.BlockedBy[0])}
		}
	}
	// Validate the whole candidate set before reserving or scheduling any item.
	// A refusal by a later service must not start an earlier service's rollout.
	for _, item := range request.Bundle.Services {
		if err := service.Configurations.ValidateCandidate(ctx, item.ID, item.Settings); err != nil {
			return models.ConfigBundleApply{}, err
		}
	}
	if service.OperationKind == "" || service.Pending == "" || service.Operations.Store == nil {
		return models.ConfigBundleApply{}, errors.New("operation service unavailable")
	}
	ids := make([]string, 0, len(request.Bundle.Services))
	for index, item := range request.Bundle.Services {
		operationID, err := configBundleOperationID()
		if err != nil {
			return models.ConfigBundleApply{}, err
		}
		expected := plan.Services[index].CurrentRevision
		operation := models.Operation{ID: operationID, Kind: service.OperationKind, State: service.Pending,
			CreatedAt: time.Now().UTC(), RequestID: requestID, Actor: actor, Resource: item.ID}
		payload := &models.OperationPayload{Version: 1, Resource: item.ID, ExpectedRevision: expected,
			SchemaVersion: plan.Services[index].SchemaVersion, Digest: request.Bundle.Digest}
		// The service configuration service owns the durable operation and rollout.
		// A service-scoped key makes retries of one bundle idempotent per item.
		reservationKey := idempotencyKey + ":" + item.ID
		command := ApplyServiceConfigurationCommand{
			InstanceID: item.ID, ExpectedRevision: expected, SchemaVersion: plan.Services[index].SchemaVersion,
			SettingsJSON:   item.Settings,
			CandidateAudit: models.AuditRecord{Actor: actor, Action: service.CandidateAction, Result: service.SucceededResult, RequestID: requestID},
			AppliedAudit:   models.AuditRecord{Actor: actor, Action: service.ApplyAction, Result: service.SucceededResult, RequestID: requestID},
			FailedAudit:    models.AuditRecord{Actor: actor, Action: service.FailedAction, Result: service.FailedResult, RequestID: requestID},
		}
		payload.Digest = payload.ComputeDigest(item.Settings)
		reserved, err := service.Configurations.Submit(ctx, command, models.OperationReservation{
			Operation: operation, Scope: "config-bundle/" + request.Project.Revision,
			Key: reservationKey, RequestDigest: payload.Digest, Payload: payload,
		})
		if err != nil {
			return models.ConfigBundleApply{}, err
		}
		ids = append(ids, reserved.ID)
	}
	return models.ConfigBundleApply{OperationIDs: ids}, nil
}

func configBundleOperationID() (string, error) {
	var bytes [24]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
