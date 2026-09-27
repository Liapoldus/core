package bootstrap

import (
	"context"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/interfaces"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/artifacts"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	"github.com/Liapoldus/core/internal/presentation/cli/caddyruntime"
)

func NewAdminMutationService(runtime caddyruntime.CaddyRuntime, checkpointStore *storage.SQLiteCaddyCheckpointStore, checkpointFiles *artifacts.CaddyCheckpointArtifacts, adminWords config.AdminMutationWords) *application.AdminMutationService {
	adminClient, _ := runtime.(interfaces.CaddyAdminClient)
	return &application.AdminMutationService{
		Admin: adminClient, Checkpoints: checkpointStore, Artifacts: checkpointFiles,
		Policy: application.AdminMutationPolicy{
			MutationMethods:       adminWords.Methods.Mutating,
			SuccessStatusMinimum:  adminWords.Statuses.SuccessMinimum,
			SuccessStatusMaximum:  adminWords.Statuses.SuccessMaximum,
			MaximumSnapshotBytes:  adminWords.Limits.SnapshotBytes,
			OperationKind:         adminWords.Operation.Kind,
			OperationRunning:      adminWords.Operation.Running,
			OperationSucceeded:    adminWords.Operation.Succeeded,
			OperationFailed:       adminWords.Operation.Failed,
			AuditAction:           adminWords.Audit.Action,
			AuditResource:         adminWords.Audit.Resource,
			AuditStarted:          adminWords.Audit.Started,
			AuditSucceeded:        adminWords.Audit.Succeeded,
			AuditFailed:           adminWords.Audit.Failed,
			InvalidConfiguration:  adminWords.Diagnostics.InvalidConfiguration,
			SnapshotUnavailable:   adminWords.Diagnostics.SnapshotUnavailable,
			CheckpointUnavailable: adminWords.Diagnostics.CheckpointUnavailable,
			AdminUnavailable:      adminWords.Diagnostics.AdminUnavailable,
		},
	}
}

func NewGroupReleaseService(artifactRoot string, groupStore *storage.SQLiteGroupStore, releaseStore *storage.SQLiteGroupReleaseStore, releaseArtifacts artifacts.GroupReleaseArtifacts, releasePolicy models.GroupReleasePolicy, runtime caddyruntime.CaddyRuntime, driftGuard *application.AdminMutationService) *application.GroupReleaseService {
	return &application.GroupReleaseService{
		Store: groupStore, Releases: releaseStore,
		ContentReader: artifacts.GroupRevisionReader{Root: artifactRoot},
		Artifacts:     releaseArtifacts, Activator: runtime, DriftGuard: driftGuard, Policy: releasePolicy,
	}
}

func ActivateCurrentGroupRelease(service *application.GroupReleaseService, runtime caddyruntime.CaddyRuntime, management config.ManagementWords, readiness, reason string) (string, string) {
	if runtime == nil {
		return readiness, reason
	}
	if err := service.Recover(context.Background()); err != nil {
		return management.Statuses.NotReady, management.Statuses.RecoveryRequired
	}
	if reason == management.Statuses.SystemReleaseRequired {
		return readiness, reason
	}
	lazy, deferred := runtime.(interface{ Active() bool })
	if deferred && lazy.Active() {
		return readiness, reason
	}
	if err := service.ActivateCurrent(context.Background()); err != nil {
		return management.Statuses.NotReady, management.Statuses.RecoveryRequired
	}
	return readiness, reason
}

func CookiePolicyManagementService(store interfaces.PluginCookiePolicyStore, runtime caddyruntime.CaddyRuntime, audit config.AuditWords, management config.ManagementWords) *application.PluginCookiePolicyService {
	if store == nil {
		return nil
	}
	var activator interfaces.PluginCookiePolicyActivator
	activator, _ = runtime.(interfaces.PluginCookiePolicyActivator)
	return &application.PluginCookiePolicyService{
		Store: store, Activator: activator,
		AuditAction:      audit.Audit.Actions.PluginCookiePolicyReplace,
		Resource:         audit.Audit.Resources.PluginCookiePolicies,
		Success:          audit.Audit.Results.Succeeded,
		Invalid:          management.Codes.InvalidCookiePolicy,
		RevisionConflict: management.Codes.CookiePolicyRevisionConflict,
	}
}
