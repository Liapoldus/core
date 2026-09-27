package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type AdminMutationPolicy struct {
	MutationMethods       []string
	SuccessStatusMinimum  int
	SuccessStatusMaximum  int
	MaximumSnapshotBytes  int64
	OperationKind         string
	OperationRunning      string
	OperationSucceeded    string
	OperationFailed       string
	AuditAction           string
	AuditResource         string
	AuditStarted          string
	AuditSucceeded        string
	AuditFailed           string
	InvalidConfiguration  string
	SnapshotUnavailable   string
	CheckpointUnavailable string
	AdminUnavailable      string
}

type AdminMutationService struct {
	Admin       interfaces.CaddyAdminClient
	Checkpoints interfaces.CaddyCheckpointStore
	Artifacts   interfaces.CheckpointArtifactStore
	Policy      AdminMutationPolicy
}

func (service *AdminMutationService) Handle(ctx context.Context, command models.AdminMutationCommand) (models.CaddyAdminResponse, error) {
	if service.Admin == nil || command.Actor == "" || command.RequestID == "" || command.Request.Method == "" || command.Request.Path == "" {
		return models.CaddyAdminResponse{}, errors.New(service.Policy.InvalidConfiguration)
	}
	if !containsAdminMethod(service.Policy.MutationMethods, command.Request.Method) {
		return service.Admin.Request(ctx, command.Request)
	}
	if service.Checkpoints == nil || service.Artifacts == nil {
		return models.CaddyAdminResponse{}, errors.New(service.Policy.InvalidConfiguration)
	}
	snapshot, err := service.Admin.Snapshot(ctx)
	if err != nil || len(snapshot) == 0 || int64(len(snapshot)) > service.Policy.MaximumSnapshotBytes {
		return models.CaddyAdminResponse{}, errors.New(service.Policy.SnapshotUnavailable)
	}
	checkpointID, err := newAdminMutationID()
	if err != nil {
		return models.CaddyAdminResponse{}, errors.New(service.Policy.CheckpointUnavailable)
	}
	operationID, err := newAdminMutationID()
	if err != nil {
		return models.CaddyAdminResponse{}, errors.New(service.Policy.CheckpointUnavailable)
	}
	digest := sha256.Sum256(snapshot)
	snapshotPath, storedDigest, err := service.Artifacts.Store(ctx, checkpointID, snapshot)
	if err != nil || storedDigest != hex.EncodeToString(digest[:]) {
		return models.CaddyAdminResponse{}, errors.New(service.Policy.CheckpointUnavailable)
	}
	createdAt := time.Now().UTC()
	checkpoint := models.CaddyCheckpoint{
		ID: checkpointID, RuntimeDigest: storedDigest, SnapshotPath: snapshotPath,
		OperationID: operationID, Actor: command.Actor, CreatedAt: createdAt,
	}
	operation := models.Operation{
		ID: operationID, Kind: service.Policy.OperationKind, State: service.Policy.OperationRunning,
		CreatedAt: createdAt, RequestID: command.RequestID, Actor: command.Actor, Resource: service.Policy.AuditResource,
	}
	startedAudit := models.AuditRecord{
		Timestamp: createdAt, Actor: command.Actor, Action: service.Policy.AuditAction,
		Resource: service.Policy.AuditResource, Result: service.Policy.AuditStarted, RequestID: command.RequestID,
		DigestBefore: storedDigest,
	}
	if err := service.Checkpoints.CreateMutation(ctx, checkpoint, operation, startedAudit); err != nil {
		_ = service.Artifacts.Delete(ctx, snapshotPath)
		return models.CaddyAdminResponse{}, errors.New(service.Policy.CheckpointUnavailable)
	}

	response, requestErr := service.Admin.Request(ctx, command.Request)
	state, result := service.Policy.OperationFailed, service.Policy.AuditFailed
	if requestErr == nil && response.Status >= service.Policy.SuccessStatusMinimum && response.Status < service.Policy.SuccessStatusMaximum {
		state, result = service.Policy.OperationSucceeded, service.Policy.AuditSucceeded
	}
	completedAt := time.Now().UTC()
	completedAudit := models.AuditRecord{
		Timestamp: completedAt, Actor: command.Actor, Action: service.Policy.AuditAction,
		Resource: service.Policy.AuditResource, Result: result, RequestID: command.RequestID,
		DigestBefore: storedDigest,
	}
	if err := service.Checkpoints.CompleteMutation(ctx, operationID, state, completedAudit); err != nil {
		return models.CaddyAdminResponse{}, errors.New(service.Policy.CheckpointUnavailable)
	}
	if requestErr != nil {
		return models.CaddyAdminResponse{}, errors.New(service.Policy.AdminUnavailable)
	}
	return response, nil
}

func containsAdminMethod(methods []string, method string) bool {
	for _, candidate := range methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func newAdminMutationID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
