-- name: list-legacy-instance-configurations
SELECT id, revision, settings_json FROM plugin_instances ORDER BY id;

-- name: select-current
SELECT active.instance_id, active.generation, active.schema_version, active.sha256, active.raw_json,
       active.slot, active.created_at,
       active.generation,
       COALESCE((SELECT generation FROM plugin_config_generations WHERE instance_id = active.instance_id AND slot = ?), 0),
       COALESCE((SELECT generation FROM plugin_config_generations WHERE instance_id = active.instance_id AND slot = ?), 0)
FROM plugin_config_generations AS active
WHERE active.instance_id = ? AND active.slot = ?;

-- name: instance-exists
SELECT EXISTS(SELECT 1 FROM plugin_instances WHERE id = ?);

-- name: operation-payload-needs-v8
SELECT sql LIKE '%expected_revision > 0%' FROM sqlite_master WHERE type = 'table' AND name = 'operation_payloads';

-- name: rebuild-operation-payloads-v8
CREATE TABLE operation_payloads_v8 (
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
ALTER TABLE operation_payloads_v8 RENAME TO operation_payloads;

-- name: select-generation
SELECT instance_id, generation, schema_version, sha256, raw_json, slot, created_at
FROM plugin_config_generations WHERE instance_id = ? AND generation = ?;

-- name: select-slots
SELECT slot, generation FROM plugin_config_generations WHERE instance_id = ?;

-- name: next-generation
SELECT COALESCE(MAX(generation), 0) + 1 FROM plugin_config_generations WHERE instance_id = ?;

-- name: insert-generation
INSERT INTO plugin_config_generations(instance_id, generation, slot, raw_json, sha256, schema_version, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: move-generation-slot
UPDATE plugin_config_generations SET slot = ?
WHERE instance_id = ? AND generation = ? AND slot = ?;

-- name: delete-slot
DELETE FROM plugin_config_generations WHERE instance_id = ? AND slot = ?;

-- name: delete-generation
DELETE FROM plugin_config_generations WHERE instance_id = ? AND generation = ? AND slot = ?;

-- name: set-operation-candidate
UPDATE operations SET result_json = ?
WHERE id = ? AND kind = ? AND resource = ? AND state = ? AND result_json IS NULL;

-- name: count-slot
SELECT COUNT(*) FROM plugin_config_generations WHERE instance_id = ? AND slot = ?;

-- name: count-generation
SELECT COUNT(*) FROM plugin_config_generations WHERE instance_id = ? AND generation = ?;

-- name: table-exists
SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?);

-- name: column-exists
SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name = ?);

-- name: legacy-configurations
SELECT r.instance_id, r.revision, r.schema_version, r.digest, r.settings_json, r.created_at,
       CASE
         WHEN r.revision = p.current_revision THEN ?
         WHEN r.revision = p.previous_revision THEN ?
         ELSE ?
       END AS slot
FROM plugin_config_pointers AS p
JOIN plugin_config_revisions AS r ON r.instance_id = p.instance_id
WHERE r.revision = p.current_revision OR r.revision = p.previous_revision OR r.revision = p.pending_revision;

-- name: legacy-configuration-integrity
SELECT COUNT(*) FROM plugin_config_pointers AS p
WHERE (p.current_revision IS NOT NULL AND NOT EXISTS (
         SELECT 1 FROM plugin_config_revisions AS r WHERE r.instance_id = p.instance_id AND r.revision = p.current_revision))
   OR (p.previous_revision IS NOT NULL AND NOT EXISTS (
         SELECT 1 FROM plugin_config_revisions AS r WHERE r.instance_id = p.instance_id AND r.revision = p.previous_revision))
   OR (p.pending_revision IS NOT NULL AND NOT EXISTS (
         SELECT 1 FROM plugin_config_revisions AS r WHERE r.instance_id = p.instance_id AND r.revision = p.pending_revision))
   OR p.current_revision = p.previous_revision OR p.current_revision = p.pending_revision
   OR p.previous_revision = p.pending_revision;

-- name: legacy-operation-configurations
SELECT p.operation_id, p.version, p.resource, p.expected_revision, p.schema_version, p.digest, p.settings_json,
       o.result_json, o.state
FROM operation_payloads AS p JOIN operations AS o ON o.id = p.operation_id
WHERE p.settings_json IS NOT NULL;

-- name: set-operation-generation
UPDATE operations SET result_json = ? WHERE id = ? AND result_json IS NULL;

-- name: drop-legacy-pointers
DROP TABLE plugin_config_pointers;

-- name: drop-legacy-revisions
DROP TABLE plugin_config_revisions;

-- name: drop-legacy-instance-settings
ALTER TABLE plugin_instances DROP COLUMN settings_json;

-- name: drop-legacy-instance-revision
ALTER TABLE plugin_instances DROP COLUMN revision;

-- name: drop-legacy-operation-settings
ALTER TABLE operation_payloads DROP COLUMN settings_json;

-- name: disable-foreign-keys
PRAGMA foreign_keys = OFF;

-- name: enable-foreign-keys
PRAGMA foreign_keys = ON;

-- name: create-plugin-instances-v9
CREATE TABLE plugin_instances_v9 (
    id TEXT PRIMARY KEY,
    manifest_json BLOB NOT NULL,
    state TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- name: copy-plugin-instances-v9
INSERT INTO plugin_instances_v9(id, manifest_json, state, updated_at)
SELECT id, manifest_json, state, updated_at FROM plugin_instances;

-- name: drop-plugin-instances-v8
DROP TABLE plugin_instances;

-- name: rename-plugin-instances-v9
ALTER TABLE plugin_instances_v9 RENAME TO plugin_instances;

-- name: foreign-key-check
PRAGMA foreign_key_check;
