package application

import (
	"sort"
	"time"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

// SelectTrafficRolloutCohorts resolves operator-selected candidate identities
// against the current authenticated replica snapshot. It never substitutes a
// replacement incarnation and excludes expired/unready replicas from traffic.
func SelectTrafficRolloutCohorts(
	plan models.TrafficRolloutPlan,
	replicas []models.TrafficRolloutReplica,
	now time.Time,
) ([]models.PluginRolloutTarget, []models.PluginRolloutTarget, error) {
	if now.IsZero() || !validSHA256(plan.ReleaseSHA256) || len(plan.Targets) == 0 || len(plan.Stages) == 0 ||
		plan.Stages[len(plan.Stages)-1].CandidateWeightPercent != 100 {
		return nil, nil, models.PluginConfigurationConflict{}
	}
	requested := make(map[string]models.TrafficRolloutTarget, len(plan.Targets))
	for _, target := range plan.Targets {
		if target.ReplicaID == "" || target.Incarnation == "" {
			return nil, nil, models.PluginConfigurationConflict{}
		}
		if _, exists := requested[target.ReplicaID]; exists {
			return nil, nil, models.PluginConfigurationConflict{}
		}
		requested[target.ReplicaID] = target
	}
	registered := make(map[string]models.TrafficRolloutReplica, len(replicas))
	for _, replica := range replicas {
		if replica.ReplicaID == "" || replica.Incarnation == "" || replica.LeaseExpiresAt.IsZero() ||
			!validSHA256(replica.ReleaseSHA256) {
			return nil, nil, models.PluginConfigurationConflict{}
		}
		if _, exists := registered[replica.ReplicaID]; exists {
			return nil, nil, models.PluginConfigurationConflict{}
		}
		registered[replica.ReplicaID] = replica
	}

	candidate := make([]models.PluginRolloutTarget, 0, len(plan.Targets))
	for replicaID, requestedTarget := range requested {
		replica, exists := registered[replicaID]
		if !exists || replica.Incarnation != requestedTarget.Incarnation || !replica.LeaseExpiresAt.After(now) ||
			!replica.Ready || replica.ReleaseSHA256 != plan.ReleaseSHA256 {
			return nil, nil, models.PluginConfigurationConflict{}
		}
		candidate = append(candidate, toPluginRolloutTarget(replica))
	}

	incumbent := make([]models.PluginRolloutTarget, 0, len(replicas))
	incumbentRelease := ""
	for replicaID, replica := range registered {
		if _, isCandidate := requested[replicaID]; isCandidate || !replica.LeaseExpiresAt.After(now) || !replica.Ready {
			continue
		}
		if incumbentRelease == "" {
			incumbentRelease = replica.ReleaseSHA256
		} else if incumbentRelease != replica.ReleaseSHA256 {
			return nil, nil, models.PluginConfigurationConflict{}
		}
		incumbent = append(incumbent, toPluginRolloutTarget(replica))
	}
	if plan.Stages[0].CandidateWeightPercent < 100 && len(incumbent) == 0 {
		return nil, nil, models.PluginConfigurationConflict{}
	}
	sort.Slice(candidate, func(left, right int) bool { return candidate[left].ReplicaID < candidate[right].ReplicaID })
	sort.Slice(incumbent, func(left, right int) bool { return incumbent[left].ReplicaID < incumbent[right].ReplicaID })
	return candidate, incumbent, nil
}

func toPluginRolloutTarget(replica models.TrafficRolloutReplica) models.PluginRolloutTarget {
	return models.PluginRolloutTarget{
		ReplicaID: replica.ReplicaID, IncarnationID: replica.Incarnation,
		ReleaseSHA256: replica.ReleaseSHA256, LeaseExpiresAt: replica.LeaseExpiresAt,
	}
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
