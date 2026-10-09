package storage

// configurationQueries returns a private set of parameterized statements.
func configurationQueries() map[string]string {
	return map[string]string{
		"list-legacy-instance-configurations": `SELECT id, revision, settings_json FROM plugin_instances ORDER BY id;`,
		"select-current": `SELECT active.instance_id, active.generation, active.schema_version, active.sha256, active.raw_json,
       active.slot, active.created_at,
       active.generation,
       COALESCE((SELECT generation FROM plugin_config_generations WHERE instance_id = active.instance_id AND slot = ?), 0),
       COALESCE((SELECT generation FROM plugin_config_generations WHERE instance_id = active.instance_id AND slot = ?), 0)
FROM plugin_config_generations AS active
WHERE active.instance_id = ? AND active.slot = ?;`,
		"instance-exists": `SELECT EXISTS(SELECT 1 FROM plugin_instances WHERE id = ?);`,
		"open-rollout-exists": `SELECT EXISTS(SELECT 1 FROM plugin_rollouts WHERE instance_id = ? AND open = 1)
    OR EXISTS (
        SELECT 1 FROM traffic_rollouts AS current
        WHERE current.instance_id = ?
          AND current.id = (
              SELECT latest.id FROM traffic_rollouts AS latest
              WHERE latest.instance_id = current.instance_id
              ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1
          )
          AND current.state <> 'completed'
    );`,
		"select-operation-rollout-identity":      `SELECT resource, state FROM operations WHERE id = ?;`,
		"select-operation-rollout-kind-identity": `SELECT kind, resource, state FROM operations WHERE id = ?;`,
		"insert-rollout": `INSERT INTO plugin_rollouts(operation_id, instance_id, generation, open, created_at)
VALUES (?, ?, ?, 1, ?);`,
		"insert-rollout-target": `INSERT INTO plugin_rollout_targets(operation_id, replica_id, incarnation_id, release_sha256, acknowledged)
VALUES (?, ?, ?, ?, 0);`,
		"insert-rollout-target-lease": `INSERT INTO plugin_rollout_target_leases(operation_id, replica_id, lease_expires_at)
VALUES (?, ?, ?);`,
		"get-rollout": `SELECT instance_id, generation, open FROM plugin_rollouts WHERE operation_id = ?;`,
		"list-rollout-targets": `SELECT targets.replica_id, targets.incarnation_id, targets.release_sha256,
       targets.acknowledged, COALESCE(leases.lease_expires_at, '')
FROM plugin_rollout_targets AS targets
LEFT JOIN plugin_rollout_target_leases AS leases USING (operation_id, replica_id)
WHERE targets.operation_id = ? ORDER BY targets.replica_id, targets.incarnation_id;`,
		"is-unacknowledged-rollout-target": `SELECT EXISTS (
    SELECT 1 FROM plugin_rollout_targets
    WHERE operation_id = ? AND replica_id = ? AND incarnation_id = ?
      AND release_sha256 = ? AND acknowledged = 0
);`,
		"select-rollout-operation-state": `SELECT operations.state, plugin_rollouts.open
FROM operations JOIN plugin_rollouts ON operations.id = plugin_rollouts.operation_id
WHERE operations.id = ?;`,
		"close-rollout-target-lost": `UPDATE plugin_rollouts SET open = 0, completed_at = ?
WHERE operation_id = ? AND open = 1;`,
		"fail-rollout-operation": `UPDATE operations SET state = ?, updated_at = ?, problem_json = ?
WHERE id = ? AND state IN (?, ?);`,
		"fail-associated-traffic-rollout": `UPDATE traffic_rollouts SET state = ?, revision = revision + 1, updated_at = ?, completed_at = ?
WHERE operation_id = ? AND state = ?;`,
		"acknowledge-rollout-target": `UPDATE plugin_rollout_targets SET acknowledged = 1
WHERE operation_id = ? AND replica_id = ? AND incarnation_id = ? AND release_sha256 = ?;`,
		"complete-rollout": `UPDATE plugin_rollouts SET open = 0, completed_at = ?
WHERE operation_id = ? AND open = 1
  AND EXISTS (SELECT 1 FROM plugin_rollout_targets WHERE operation_id = plugin_rollouts.operation_id)
  AND NOT EXISTS (SELECT 1 FROM plugin_rollout_targets WHERE operation_id = plugin_rollouts.operation_id AND acknowledged = 0);`,
		"active-rollout-for-instance": `SELECT EXISTS(SELECT 1 FROM plugin_rollouts WHERE instance_id = ? AND open = 1);`,
		"operation-payload-needs-v8":  `SELECT sql LIKE '%expected_revision > 0%' FROM sqlite_master WHERE type = 'table' AND name = 'operation_payloads';`,
		"rebuild-operation-payloads-v8": `CREATE TABLE operation_payloads_v8 (
    operation_id TEXT PRIMARY KEY REFERENCES operations(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    resource TEXT NOT NULL,
    expected_revision INTEGER NOT NULL CHECK (expected_revision >= 0),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    digest TEXT NOT NULL
);
INSERT INTO operation_payloads_v8(operation_id, version, resource, expected_revision, schema_version, digest)
SELECT operation_id, version, resource, expected_revision, schema_version, digest FROM operation_payloads;
DROP TABLE operation_payloads;
ALTER TABLE operation_payloads_v8 RENAME TO operation_payloads;`,
		"select-generation": `SELECT instance_id, generation, schema_version, sha256, raw_json, slot, created_at
FROM plugin_config_generations WHERE instance_id = ? AND generation = ?;`,
		"select-slots":    `SELECT slot, generation FROM plugin_config_generations WHERE instance_id = ?;`,
		"next-generation": `SELECT COALESCE(MAX(generation), 0) + 1 FROM plugin_config_generations WHERE instance_id = ?;`,
		"insert-generation": `INSERT INTO plugin_config_generations(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);`,
		"move-generation-slot": `UPDATE plugin_config_generations SET slot = ?
WHERE instance_id = ? AND generation = ? AND slot = ?;`,
		"delete-slot":       `DELETE FROM plugin_config_generations WHERE instance_id = ? AND slot = ?;`,
		"delete-generation": `DELETE FROM plugin_config_generations WHERE instance_id = ? AND generation = ? AND slot = ?;`,
		"set-operation-candidate": `UPDATE operations SET result_json = ?
WHERE id = ? AND kind = ? AND resource = ? AND state = ? AND result_json IS NULL;`,
		"count-slot":       `SELECT COUNT(*) FROM plugin_config_generations WHERE instance_id = ? AND slot = ?;`,
		"count-generation": `SELECT COUNT(*) FROM plugin_config_generations WHERE instance_id = ? AND generation = ?;`,
		"table-exists":     `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?);`,
		"column-exists":    `SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name = ?);`,
		"legacy-configurations": `SELECT r.instance_id, r.revision, r.schema_version, r.digest, r.settings_json, r.created_at,
       CASE
         WHEN r.revision = p.current_revision THEN ?
         WHEN r.revision = p.previous_revision THEN ?
         ELSE ?
       END AS slot
FROM plugin_config_pointers AS p
JOIN plugin_config_revisions AS r ON r.instance_id = p.instance_id
WHERE r.revision = p.current_revision OR r.revision = p.previous_revision OR r.revision = p.pending_revision;`,
		"legacy-configuration-integrity": `SELECT COUNT(*) FROM plugin_config_pointers AS p
WHERE (p.current_revision IS NOT NULL AND NOT EXISTS (
         SELECT 1 FROM plugin_config_revisions AS r WHERE r.instance_id = p.instance_id AND r.revision = p.current_revision))
   OR (p.previous_revision IS NOT NULL AND NOT EXISTS (
         SELECT 1 FROM plugin_config_revisions AS r WHERE r.instance_id = p.instance_id AND r.revision = p.previous_revision))
   OR (p.pending_revision IS NOT NULL AND NOT EXISTS (
         SELECT 1 FROM plugin_config_revisions AS r WHERE r.instance_id = p.instance_id AND r.revision = p.pending_revision))
   OR p.current_revision = p.previous_revision OR p.current_revision = p.pending_revision
   OR p.previous_revision = p.pending_revision;`,
		"legacy-operation-configurations": `SELECT p.operation_id, p.version, p.resource, p.expected_revision, p.schema_version, p.digest, p.settings_json,
       o.result_json, o.state
FROM operation_payloads AS p JOIN operations AS o ON o.id = p.operation_id
WHERE p.settings_json IS NOT NULL;`,
		"set-operation-generation":       `UPDATE operations SET result_json = ? WHERE id = ? AND result_json IS NULL;`,
		"drop-legacy-pointers":           `DROP TABLE plugin_config_pointers;`,
		"drop-legacy-revisions":          `DROP TABLE plugin_config_revisions;`,
		"drop-legacy-instance-settings":  `ALTER TABLE plugin_instances DROP COLUMN settings_json;`,
		"drop-legacy-instance-revision":  `ALTER TABLE plugin_instances DROP COLUMN revision;`,
		"drop-legacy-operation-settings": `ALTER TABLE operation_payloads DROP COLUMN settings_json;`,
		"disable-foreign-keys":           `PRAGMA foreign_keys = OFF;`,
		"enable-foreign-keys":            `PRAGMA foreign_keys = ON;`,
		"create-plugin-instances-v9": `CREATE TABLE plugin_instances_v9 (
    id TEXT PRIMARY KEY,
    manifest_json BLOB NOT NULL,
    state TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);`,
		"copy-plugin-instances-v9": `INSERT INTO plugin_instances_v9(id, manifest_json, state, updated_at)
SELECT id, manifest_json, state, updated_at FROM plugin_instances;`,
		"drop-plugin-instances-v8":   `DROP TABLE plugin_instances;`,
		"rename-plugin-instances-v9": `ALTER TABLE plugin_instances_v9 RENAME TO plugin_instances;`,
		"foreign-key-check":          `PRAGMA foreign_key_check;`,
	}
}
