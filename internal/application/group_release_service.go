package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
)

type GroupReleaseService struct {
	Store         interfaces.GroupStore
	Releases      interfaces.GroupReleaseStore
	ContentReader interfaces.GroupRevisionContentReader
	Artifacts     interfaces.GroupReleaseArtifactStore
	Activator     interfaces.CaddySnapshotActivator
	Policy        models.GroupReleasePolicy
}

var releaseActivationLock sync.Mutex

func (service *GroupReleaseService) Accept(ctx context.Context, command models.GroupReleaseCommand) (models.Operation, bool, error) {
	if service == nil {
		return models.Operation{}, false, models.GroupReleaseValidationError{}
	}
	if service.Store == nil || service.Releases == nil || service.ContentReader == nil || service.Artifacts == nil || service.Activator == nil || command.GroupID == "" || command.Actor == "" || command.RequestID == "" || command.IdempotencyKey == "" || command.IdempotencyScope == "" || len(command.Caddyfile) == 0 {
		return models.Operation{}, false, models.GroupReleaseValidationError{Cause: errors.New(service.Policy.InvalidConfiguration)}
	}
	group, err := service.Store.GetGroup(ctx, command.GroupID)
	if err != nil {
		return models.Operation{}, false, err
	}
	if !group.Active {
		return models.Operation{}, false, models.GroupNotFound{}
	}
	operationID, err := randomDigestID()
	if err != nil {
		return models.Operation{}, false, err
	}
	revisionID, err := randomDigestID()
	if err != nil {
		return models.Operation{}, false, err
	}
	revision, err := service.Artifacts.StageCaddyfile(ctx, revisionID, command.Caddyfile)
	if err != nil {
		return models.Operation{}, false, err
	}
	revision.GroupID = command.GroupID
	revision.Actor = command.Actor
	if len(command.Artifact) > 0 {
		artifactRevision, stageErr := service.Artifacts.StageArtifact(ctx, revisionID, command.Artifact, service.Policy)
		if stageErr != nil {
			_ = service.Artifacts.DiscardCaddyfile(ctx, revision)
			return models.Operation{}, false, stageErr
		}
		revision.ArtifactDigest = artifactRevision.ArtifactDigest
		revision.ArtifactPath = artifactRevision.ArtifactPath
	}
	command.Now = time.Now().UTC()
	if command.IdempotencyWindow <= 0 {
		command.IdempotencyWindow = service.Policy.IdempotencyWindow
	}
	candidate, err := service.compose(ctx, command.GroupID, &revision)
	if err != nil {
		service.discard(ctx, revision)
		return models.Operation{}, false, err
	}
	if err := service.Activator.Validate(ctx, candidate); err != nil {
		service.discard(ctx, revision)
		return models.Operation{}, false, models.GroupReleaseValidationError{Cause: err}
	}
	keyHash := sha256.Sum256([]byte(command.IdempotencyKey))
	requestHash := sha256.New()
	_, _ = requestHash.Write([]byte(command.GroupID))
	_, _ = requestHash.Write([]byte{0})
	if command.ExpectedCurrentRevision != nil {
		_, _ = requestHash.Write([]byte(*command.ExpectedCurrentRevision))
	}
	_, _ = requestHash.Write([]byte{0})
	_, _ = requestHash.Write(command.Caddyfile)
	_, _ = requestHash.Write([]byte{0})
	_, _ = requestHash.Write(command.Artifact)
	reservation := models.GroupReleaseReservation{
		GroupID: command.GroupID, ExpectedCurrentRevision: command.ExpectedCurrentRevision,
		Actor: command.Actor, Scope: command.IdempotencyScope, KeyDigest: hex.EncodeToString(keyHash[:]),
		RequestDigest: hex.EncodeToString(requestHash.Sum(nil)), OperationID: operationID,
		RevisionID: revision.ID, OperationKind: service.Policy.OperationKind,
		OperationState: service.Policy.PendingState, RequestID: command.RequestID,
		CaddyfileDigest: revision.CaddyfileDigest, CaddyfilePath: revision.CaddyfilePath,
		ArtifactDigest: revision.ArtifactDigest, ArtifactPath: revision.ArtifactPath,
		CreatedAt: command.Now, ExpiresAt: command.Now.Add(command.IdempotencyWindow),
	}
	operation, duplicate, err := service.Releases.Reserve(ctx, reservation)
	if err != nil {
		service.discard(ctx, revision)
		return models.Operation{}, false, err
	}
	if duplicate {
		service.discard(ctx, revision)
		return operation, true, nil
	}
	go service.execute(reservation, revision)
	return operation, false, nil
}

func (service *GroupReleaseService) discard(ctx context.Context, revision models.GroupRevision) {
	_ = service.Artifacts.DiscardCaddyfile(ctx, revision)
	if revision.ArtifactPath != nil {
		_ = service.Artifacts.DiscardArtifact(ctx, revision)
	}
}

func (service *GroupReleaseService) Recover(ctx context.Context) error {
	reservations, err := service.Releases.Pending(ctx)
	if err != nil {
		return err
	}
	if len(reservations) == 0 {
		return nil
	}
	releaseActivationLock.Lock()
	defer releaseActivationLock.Unlock()
	activeSnapshot, err := service.compose(ctx, "", nil)
	if err != nil {
		return err
	}
	if err := service.Activator.Activate(ctx, activeSnapshot); err != nil {
		return err
	}
	for _, reservation := range reservations {
		record := models.AuditRecord{
			Timestamp: time.Now().UTC(), Actor: reservation.Actor, Action: service.Policy.AuditAction,
			Resource: reservation.GroupID, Result: service.Policy.FailureResult, RequestID: reservation.RequestID,
		}
		if err := service.Releases.Fail(ctx, reservation.OperationID, service.Policy.FailedState, service.Policy.JournalFailedState, record); err != nil {
			return err
		}
		service.discard(ctx, models.GroupRevision{
			ID: reservation.RevisionID, CaddyfilePath: reservation.CaddyfilePath,
			ArtifactPath: reservation.ArtifactPath,
		})
	}
	return nil
}

func (service *GroupReleaseService) ActivateCurrent(ctx context.Context) error {
	releaseActivationLock.Lock()
	defer releaseActivationLock.Unlock()
	snapshot, err := service.compose(ctx, "", nil)
	if err != nil {
		return err
	}
	return service.Activator.Activate(ctx, snapshot)
}

func (service *GroupReleaseService) execute(reservation models.GroupReleaseReservation, revision models.GroupRevision) {
	ctx := context.Background()
	releaseActivationLock.Lock()
	defer releaseActivationLock.Unlock()
	candidate, err := service.compose(ctx, reservation.GroupID, &revision)
	if err == nil {
		err = service.Activator.Activate(ctx, candidate)
	}
	if err != nil {
		service.fail(ctx, reservation, nil)
		return
	}
	var beforeDigest string
	if reservation.ExpectedCurrentRevision != nil {
		beforeDigest = *reservation.ExpectedCurrentRevision
	}
	record := models.AuditRecord{
		Timestamp: time.Now().UTC(), Actor: reservation.Actor, Action: service.Policy.AuditAction,
		Resource: reservation.GroupID, Result: service.Policy.SuccessResult, RequestID: reservation.RequestID,
		DigestBefore: beforeDigest, DigestAfter: revision.CaddyfileDigest,
	}
	err = service.Releases.Commit(ctx, models.GroupReleaseCommit{
		GroupID: reservation.GroupID, ExpectedCurrentRevision: reservation.ExpectedCurrentRevision,
		Revision: revision, OperationID: reservation.OperationID, OperationState: service.Policy.SucceededState,
		JournalState: service.Policy.JournalCompleteState, UpdatedAt: time.Now().UTC(), Audit: record,
	})
	if err == nil {
		return
	}
	previousSnapshot, rollbackErr := service.compose(ctx, "", nil)
	if rollbackErr == nil {
		rollbackErr = service.Activator.Activate(ctx, previousSnapshot)
	}
	if rollbackErr != nil {
		return
	}
	service.fail(ctx, reservation, &record)
}

func (service *GroupReleaseService) fail(ctx context.Context, reservation models.GroupReleaseReservation, successRecord *models.AuditRecord) {
	record := models.AuditRecord{
		Timestamp: time.Now().UTC(), Actor: reservation.Actor, Action: service.Policy.AuditAction,
		Resource: reservation.GroupID, Result: service.Policy.FailureResult, RequestID: reservation.RequestID,
	}
	if successRecord != nil {
		record.DigestBefore = successRecord.DigestBefore
		record.DigestAfter = successRecord.DigestAfter
	}
	if err := service.Releases.Fail(ctx, reservation.OperationID, service.Policy.FailedState, service.Policy.JournalFailedState, record); err == nil {
		service.discard(ctx, models.GroupRevision{
			ID: reservation.RevisionID, CaddyfilePath: reservation.CaddyfilePath,
			ArtifactPath: reservation.ArtifactPath,
		})
	}
}

func (service *GroupReleaseService) compose(ctx context.Context, candidateGroupID string, candidate *models.GroupRevision) ([]byte, error) {
	groups, err := service.Store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(groups.Items, func(i, j int) bool {
		return groups.Items[i].ID == service.Policy.SystemGroupID && groups.Items[j].ID != service.Policy.SystemGroupID
	})
	parts := make([][]byte, 0, len(groups.Items))
	candidateFound := false
	for _, group := range groups.Items {
		if !group.Active {
			continue
		}
		if group.ID == candidateGroupID && candidate != nil {
			detail, err := service.ContentReader.Read(ctx, *candidate)
			if err != nil {
				return nil, err
			}
			parts = append(parts, []byte(detail.Caddyfile))
			candidateFound = true
			continue
		}
		if group.CurrentRevisionID == nil {
			continue
		}
		revision, err := service.Store.GetRevision(ctx, group.ID, *group.CurrentRevisionID)
		if err != nil {
			return nil, err
		}
		detail, err := service.ContentReader.Read(ctx, revision)
		if err != nil {
			return nil, err
		}
		parts = append(parts, []byte(detail.Caddyfile))
	}
	if candidate != nil && !candidateFound {
		return nil, models.GroupNotFound{}
	}
	length := 0
	for _, part := range parts {
		length += len(part)
	}
	if len(parts) > 1 {
		length += (len(parts) - 1) * len(service.Policy.FragmentSeparator)
	}
	combined := make([]byte, 0, length)
	for index, part := range parts {
		if index > 0 {
			combined = append(combined, service.Policy.FragmentSeparator...)
		}
		combined = append(combined, part...)
	}
	return combined, nil
}

func randomDigestID() (string, error) {
	value := make([]byte, sha256.Size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
