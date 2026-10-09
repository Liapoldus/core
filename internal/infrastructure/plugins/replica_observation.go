package plugins

import (
	"context"
	"strconv"

	"github.com/Liapoldus/core/internal/domain/models"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
)

// SDKReplicaReadinessClient reports the exact generation applied by one
// authenticated replica.
type SDKReplicaReadinessClient interface {
	Readiness(context.Context) (sdkmodels.Readiness, error)
}

func readinessMatches(readiness sdkmodels.Readiness, instanceID, replicaID string, active models.PluginConfigurationRevision) bool {
	return readiness.Ready &&
		readiness.Generation == strconv.FormatInt(active.Revision, 10) &&
		readiness.SHA256 == active.Digest &&
		readiness.SchemaVersion == strconv.FormatInt(active.SchemaVersion, 10) &&
		readiness.InstanceID == instanceID &&
		readiness.ReplicaID == replicaID
}

func (applier *SDKConfigurationApplier) recordOutcome(ctx context.Context, instanceID, replicaID string, generation int64, acknowledged, unreachable bool) {
	if applier.Observations != nil {
		applier.Observations.RecordReplicaObservation(ctx, instanceID, replicaID, generation, acknowledged, unreachable)
	}
}
