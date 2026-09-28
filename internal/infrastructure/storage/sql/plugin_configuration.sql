-- name: list-instances
SELECT id, revision, settings_json FROM plugin_instances ORDER BY id;

-- name: insert-revision
INSERT OR IGNORE INTO plugin_config_revisions
    (instance_id, revision, schema_version, digest, settings_json, state, actor, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: insert-pointer
INSERT OR IGNORE INTO plugin_config_pointers(instance_id, current_revision)
VALUES (?, ?);

-- name: select-pointers
SELECT instance_id, current_revision, previous_revision, pending_revision
FROM plugin_config_pointers WHERE instance_id = ?;

-- name: select-current
SELECT r.instance_id, r.revision, r.schema_version, r.digest, r.settings_json, r.state, r.actor, r.created_at,
       p.current_revision, p.previous_revision, p.pending_revision
FROM plugin_config_pointers AS p
JOIN plugin_config_revisions AS r
  ON r.instance_id = p.instance_id AND r.revision = p.current_revision
WHERE p.instance_id = ?;

-- name: select-revision
SELECT instance_id, revision, schema_version, digest, settings_json, state, actor, created_at
FROM plugin_config_revisions WHERE instance_id = ? AND revision = ?;

-- name: next-revision
SELECT COALESCE(MAX(revision), 0) + 1 FROM plugin_config_revisions WHERE instance_id = ?;

-- name: insert-candidate
INSERT INTO plugin_config_revisions
    (instance_id, revision, schema_version, digest, settings_json, state, actor, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: set-pending
UPDATE plugin_config_pointers SET pending_revision = ?, updated_at = ?
WHERE instance_id = ? AND current_revision IS ? AND pending_revision IS NULL;

-- name: set-pointers
UPDATE plugin_config_pointers
SET current_revision = ?, previous_revision = ?, pending_revision = ?, updated_at = ?
WHERE instance_id = ?;

-- name: set-revision-state
UPDATE plugin_config_revisions SET state = ?
WHERE instance_id = ? AND revision = ? AND state = ?;

-- name: update-instance-settings
UPDATE plugin_instances SET settings_json = ?, revision = ?, updated_at = ?
WHERE id = ?;
