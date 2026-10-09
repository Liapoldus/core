package storage

// trafficRolloutQueries returns a private set of parameterized statements.
func trafficRolloutQueries() map[string]string {
	return map[string]string{
		"traffic-rollout-create": `INSERT INTO traffic_rollouts (
    id, operation_id, instance_id, generation, release_sha256, plan_json,
    state, active_stage_index, controller_revision, revision, created_at, updated_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		"traffic-rollout-instance-exists": `SELECT EXISTS(SELECT 1 FROM plugin_instances WHERE id = ?);`,
		"traffic-rollout-active-generation": `SELECT COALESCE((SELECT generation FROM plugin_config_generations WHERE instance_id = ? AND slot = ?), 0),
       COALESCE((SELECT sha256 FROM plugin_config_generations WHERE instance_id = ? AND slot = ?), '');`,
		"traffic-rollout-staging-count":   `SELECT COUNT(*) FROM plugin_config_generations WHERE instance_id = ? AND slot = ?;`,
		"traffic-rollout-next-generation": `SELECT COALESCE(MAX(generation), 0) + 1 FROM plugin_config_generations WHERE instance_id = ?;`,
		"traffic-rollout-insert-generation": `INSERT INTO plugin_config_generations(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);`,
		"traffic-rollout-delete-previous":      `DELETE FROM plugin_config_generations WHERE instance_id = ? AND slot = ?;`,
		"traffic-rollout-move-generation-slot": `UPDATE plugin_config_generations SET slot = ? WHERE instance_id = ? AND generation = ? AND slot = ?;`,
		"traffic-rollout-plugin-rollout-create": `INSERT INTO plugin_rollouts(operation_id, instance_id, generation, open, created_at)
VALUES (?, ?, ?, 1, ?);`,
		"traffic-rollout-plugin-target-create": `INSERT INTO plugin_rollout_targets(operation_id, replica_id, incarnation_id, release_sha256, acknowledged)
VALUES (?, ?, ?, ?, 0);`,
		"traffic-rollout-plugin-target-lease-create": `INSERT INTO plugin_rollout_target_leases(operation_id, replica_id, lease_expires_at)
VALUES (?, ?, ?);`,
		"traffic-rollout-stage-create": `INSERT INTO traffic_rollout_stages (
    operation_id, stage_index, stage_id, candidate_weight_percent,
    minimum_observation_seconds, require_manual_approval, state,
    stage_started_at, confirmed_at, applied_candidate_weight_percent,
    controller_revision, approved_by, approved_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		"traffic-rollout-target-create": `INSERT INTO traffic_rollout_cohort_targets (
    operation_id, cohort, replica_id, incarnation_id, release_sha256, lease_expires_at
) VALUES (?, ?, ?, ?, ?, ?);`,
		"traffic-rollout-select": `SELECT id, operation_id, instance_id, generation, release_sha256, plan_json,
       state, active_stage_index, COALESCE(controller_revision, ''), revision,
       created_at, updated_at, COALESCE(completed_at, '')
FROM traffic_rollouts WHERE id = ?;`,
		"traffic-rollout-open-configurations": `SELECT t.operation_id, t.instance_id, t.generation
FROM traffic_rollouts AS t
JOIN plugin_rollouts AS p ON p.operation_id = t.operation_id
WHERE t.instance_id = ? AND p.open = 1
ORDER BY t.created_at, t.operation_id;`,
		"traffic-rollout-latest-state": `SELECT state FROM traffic_rollouts
WHERE instance_id = ?
ORDER BY created_at DESC, id DESC
LIMIT 1;`,
		"traffic-rollout-controller-visible": `SELECT t.id
FROM traffic_rollouts AS t
JOIN plugin_rollouts AS p ON p.operation_id = t.operation_id
JOIN operations AS o ON o.id = t.operation_id
WHERE p.open = 0 AND t.state = 'running' AND o.state IN ('pending', 'running')
ORDER BY t.created_at, t.id;`,
		"traffic-rollout-stages-select": `SELECT stage_index, stage_id, candidate_weight_percent, minimum_observation_seconds,
       require_manual_approval, state, COALESCE(stage_started_at, ''),
       COALESCE(confirmed_at, ''), COALESCE(applied_candidate_weight_percent, -1),
       COALESCE(controller_revision, ''), COALESCE(approved_by, ''), COALESCE(approved_at, '')
FROM traffic_rollout_stages WHERE operation_id = ? ORDER BY stage_index;`,
		"traffic-rollout-targets-select": `SELECT cohort, replica_id, incarnation_id, release_sha256, lease_expires_at
FROM traffic_rollout_cohort_targets WHERE operation_id = ? ORDER BY cohort, replica_id, incarnation_id;`,
		"traffic-rollout-current-state": `SELECT operation_id, instance_id, state, active_stage_index, revision
FROM traffic_rollouts WHERE id = ?;`,
		"traffic-rollout-current-stage": `SELECT stage_id, candidate_weight_percent, minimum_observation_seconds,
       require_manual_approval, state, COALESCE(confirmed_at, ''),
       COALESCE(applied_candidate_weight_percent, -1)
FROM traffic_rollout_stages WHERE operation_id = ? AND stage_index = ?;`,
		"traffic-rollout-stage-count": `SELECT COUNT(*) FROM traffic_rollout_stages WHERE operation_id = ?;`,
		"traffic-rollout-next-stage-id": `SELECT stage_id FROM traffic_rollout_stages
WHERE operation_id = ? AND stage_index = ? AND state = 'pending';`,
		"traffic-rollout-confirm-stage": `UPDATE traffic_rollout_stages
SET state = 'confirmed', confirmed_at = ?, applied_candidate_weight_percent = ?, controller_revision = ?
WHERE operation_id = ? AND stage_index = ? AND stage_id = ? AND state = 'active'
  AND candidate_weight_percent = ?;`,
		"traffic-rollout-confirmation-select": `SELECT request_digest, response_json FROM traffic_rollout_confirmations
WHERE controller_identity = ? AND rollout_id = ? AND key_digest = ?;`,
		"traffic-rollout-confirmation-insert": `INSERT INTO traffic_rollout_confirmations(controller_identity, rollout_id, key_digest, request_digest, response_json, created_at)
VALUES (?, ?, ?, ?, ?, ?);`,
		"traffic-rollout-update-stage": `UPDATE traffic_rollout_stages SET state = ?, stage_started_at = ?, approved_by = ?, approved_at = ?
WHERE operation_id = ? AND stage_index = ? AND stage_id = ? AND state = ?;`,
		"traffic-rollout-update-state": `UPDATE traffic_rollouts SET state = ?, active_stage_index = ?, revision = revision + 1,
       controller_revision = COALESCE(NULLIF(?, ''), controller_revision), updated_at = ?, completed_at = ?
WHERE id = ? AND revision = ? AND state = 'running' AND active_stage_index = ?;`,
		"traffic-rollout-complete-operation": `UPDATE operations SET state = 'completed', updated_at = ?
WHERE id = ? AND state = 'running';`,
		"traffic-rollout-start-operation": `UPDATE operations SET state = 'running', updated_at = ?
WHERE id = ? AND state = 'pending';`,
		"traffic-rollout-operation-state": `SELECT state FROM operations WHERE id = ?;`,
	}
}
