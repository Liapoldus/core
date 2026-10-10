package main

import (
	"encoding/json"
	"os"
	"time"

	"github.com/Liapoldus/core/v3/internal/application"
	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func main() {
	if len(os.Args) != 2 && len(os.Args) != 3 {
		os.Exit(2)
	}
	plan, err := application.ParseTrafficRolloutPlan([]byte(os.Args[1]))
	result := map[string]any{"valid": err == nil}
	if err == nil && len(plan.Stages) > 0 {
		result["requireManualApproval"] = plan.Stages[0].RequireManualApproval
	}
	if err == nil && len(os.Args) == 3 {
		var input []fixtureReplica
		if json.Unmarshal([]byte(os.Args[2]), &input) != nil {
			result["valid"] = false
		} else {
			now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
			replicas := make([]models.TrafficRolloutReplica, 0, len(input))
			for _, item := range input {
				replicas = append(replicas, models.TrafficRolloutReplica{
					ReplicaID: item.ReplicaID, Incarnation: item.Incarnation,
					ReleaseSHA256: item.ReleaseSHA256, Ready: item.Ready,
					LeaseExpiresAt: now.Add(time.Duration(item.LeaseExpiresInSeconds) * time.Second),
				})
			}
			candidate, incumbent, selectErr := application.SelectTrafficRolloutCohorts(plan, replicas, now)
			if selectErr != nil {
				result["valid"] = false
			} else {
				result["candidateTargets"] = identities(candidate)
				result["incumbentTargets"] = identities(incumbent)
			}
		}
	}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		os.Exit(2)
	}
}

type fixtureReplica struct {
	ReplicaID             string `json:"replicaId"`
	Incarnation           string `json:"incarnation"`
	ReleaseSHA256         string `json:"releaseSha256"`
	Ready                 bool   `json:"ready"`
	LeaseExpiresInSeconds int    `json:"leaseExpiresInSeconds"`
}

func identities(targets []models.PluginRolloutTarget) []map[string]string {
	result := make([]map[string]string, 0, len(targets))
	for _, target := range targets {
		result = append(result, map[string]string{"replicaId": target.ReplicaID, "incarnation": target.IncarnationID})
	}
	return result
}
