package storage

// SchemaSQL preserves the ordered SQLite migration history.
const SchemaSQL = `BEGIN IMMEDIATE;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plugin_instances (
    id TEXT PRIMARY KEY,
    manifest_json BLOB NOT NULL,
    state TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plugin_config_generations (
    instance_id TEXT NOT NULL REFERENCES plugin_instances(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL CHECK (generation > 0),
    slot TEXT NOT NULL CHECK (slot IN ('active', 'previous', 'staging')),
    raw_json BLOB NOT NULL CHECK (json_valid(raw_json) AND json_type(raw_json) = 'object'),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    created_at TEXT NOT NULL,
    PRIMARY KEY (instance_id, generation),
    UNIQUE (instance_id, slot)
);

-- Replica rows are observations of declared replicas, never a declaration. The
-- replica set, each endpoint and each expected peer identity are owned by the
-- deployment/runtime registration layer, so this table intentionally has no endpoint, identity or
-- composition column: it only records what a replica was last observed doing so
-- readiness and drift can be derived without Core re-deriving desired topology.
CREATE TABLE IF NOT EXISTS plugin_replicas (
    instance_id TEXT NOT NULL REFERENCES plugin_instances(id) ON DELETE CASCADE,
    replica_id TEXT NOT NULL,
    observed_generation INTEGER,
    observed_state TEXT NOT NULL CHECK (observed_state IN ('pending', 'acknowledged', 'failed', 'unreachable')),
    last_failure_code TEXT,
    observed_at TEXT NOT NULL,
    PRIMARY KEY (instance_id, replica_id)
);

-- This durable marker distinguishes instances that entered v2 self-registration
-- from legacy operator-declared endpoints. Live replica endpoints and leases
-- remain in memory and are never restored from this table.
CREATE TABLE IF NOT EXISTS plugin_registered_instances (
    instance_id TEXT PRIMARY KEY REFERENCES plugin_instances(id) ON DELETE CASCADE,
    registered_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- A rollout freezes its selected incarnation cohort in the same transaction
-- that promotes the desired configuration. Completed rows preserve the audit
-- record; only an open rollout blocks another registered-instance mutation.
CREATE TABLE IF NOT EXISTS plugin_rollouts (
    operation_id TEXT PRIMARY KEY REFERENCES operations(id) ON DELETE RESTRICT,
    instance_id TEXT NOT NULL REFERENCES plugin_instances(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL CHECK (generation > 0),
    open INTEGER NOT NULL CHECK (open IN (0, 1)),
    created_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS one_open_plugin_rollout_per_instance
    ON plugin_rollouts(instance_id) WHERE open = 1;

CREATE TABLE IF NOT EXISTS plugin_rollout_targets (
    operation_id TEXT NOT NULL REFERENCES plugin_rollouts(operation_id) ON DELETE CASCADE,
    replica_id TEXT NOT NULL,
    incarnation_id TEXT NOT NULL,
    release_sha256 TEXT NOT NULL CHECK (length(release_sha256) = 64),
    acknowledged INTEGER NOT NULL DEFAULT 0 CHECK (acknowledged IN (0, 1)),
    PRIMARY KEY (operation_id, replica_id)
);
CREATE TABLE IF NOT EXISTS plugin_rollout_target_leases (
    operation_id TEXT NOT NULL,
    replica_id TEXT NOT NULL,
    lease_expires_at TEXT NOT NULL,
    PRIMARY KEY (operation_id, replica_id),
    FOREIGN KEY (operation_id, replica_id)
        REFERENCES plugin_rollout_targets(operation_id, replica_id) ON DELETE CASCADE
);

-- Traffic rollout intent is durable independently from plugin config rollout
-- ACKs. The exact candidate and incumbent cohorts and immutable stage plan are
-- retained for controller reconciliation and restart recovery.
CREATE TABLE IF NOT EXISTS traffic_rollouts (
    id TEXT PRIMARY KEY,
    operation_id TEXT NOT NULL UNIQUE REFERENCES operations(id) ON DELETE RESTRICT,
    instance_id TEXT NOT NULL REFERENCES plugin_instances(id) ON DELETE CASCADE,
    generation INTEGER NOT NULL CHECK (generation > 0),
    release_sha256 TEXT NOT NULL CHECK (length(release_sha256) = 64),
    plan_json BLOB NOT NULL CHECK (json_valid(plan_json) AND json_type(plan_json) = 'object'),
    state TEXT NOT NULL CHECK (state IN ('running', 'paused', 'completed', 'failed')),
    active_stage_index INTEGER NOT NULL CHECK (active_stage_index >= 0),
    controller_revision TEXT,
    revision INTEGER NOT NULL CHECK (revision > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS one_open_traffic_rollout_per_instance
    ON traffic_rollouts(instance_id) WHERE state IN ('running', 'paused');

CREATE TABLE IF NOT EXISTS traffic_rollout_stages (
    operation_id TEXT NOT NULL REFERENCES traffic_rollouts(operation_id) ON DELETE CASCADE,
    stage_index INTEGER NOT NULL CHECK (stage_index >= 0),
    stage_id TEXT NOT NULL,
    candidate_weight_percent INTEGER NOT NULL CHECK (candidate_weight_percent BETWEEN 1 AND 100),
    minimum_observation_seconds INTEGER NOT NULL CHECK (minimum_observation_seconds >= 0),
    require_manual_approval INTEGER NOT NULL CHECK (require_manual_approval IN (0, 1)),
    state TEXT NOT NULL CHECK (state IN ('pending', 'active', 'confirmed', 'approved', 'completed')),
    stage_started_at TEXT,
    confirmed_at TEXT,
    applied_candidate_weight_percent INTEGER CHECK (applied_candidate_weight_percent BETWEEN 0 AND 100),
    controller_revision TEXT,
    approved_by TEXT,
    approved_at TEXT,
    PRIMARY KEY (operation_id, stage_index),
    UNIQUE (operation_id, stage_id)
);

CREATE TABLE IF NOT EXISTS traffic_rollout_cohort_targets (
    operation_id TEXT NOT NULL REFERENCES traffic_rollouts(operation_id) ON DELETE CASCADE,
    cohort TEXT NOT NULL CHECK (cohort IN ('candidate', 'incumbent')),
    replica_id TEXT NOT NULL,
    incarnation_id TEXT NOT NULL,
    release_sha256 TEXT NOT NULL CHECK (length(release_sha256) = 64),
    lease_expires_at TEXT NOT NULL,
    PRIMARY KEY (operation_id, cohort, replica_id)
);

CREATE TABLE IF NOT EXISTS traffic_rollout_confirmations (
    controller_identity TEXT NOT NULL,
    rollout_id TEXT NOT NULL REFERENCES traffic_rollouts(id) ON DELETE CASCADE,
    key_digest TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    response_json BLOB NOT NULL CHECK (json_valid(response_json) AND json_type(response_json) = 'object'),
    created_at TEXT NOT NULL,
    PRIMARY KEY (controller_identity, rollout_id, key_digest)
);

-- Peer link policy is Core-owned, deny-by-default desired state for one
-- caller→target instance pair. Rules are stored as a JSON array of generic
-- placement/carrier entries; Core never interprets product content. The pair
-- carries a monotonic revision used for optimistic concurrency (ETag/If-Match).
-- Unknown instance IDs are valid so an operator can pre-authorize a link.
CREATE TABLE IF NOT EXISTS plugin_link_policies (
    caller_instance_id TEXT NOT NULL,
    target_instance_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    rules_json BLOB NOT NULL CHECK (json_valid(rules_json) AND json_type(rules_json) = 'array'),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (caller_instance_id, target_instance_id)
);

-- v1 never installs, starts or supervises a plugin process, so the launch
-- settings that backed local mode are removed rather than carried forward.
DROP TABLE IF EXISTS plugin_launch_settings;

CREATE INDEX IF NOT EXISTS plugin_config_generations_by_instance
    ON plugin_config_generations(instance_id, generation DESC);

CREATE TABLE IF NOT EXISTS service_keys (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    verifier BLOB NOT NULL,
    role TEXT NOT NULL CHECK (role = 'platform-admin'),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TEXT,
    revoked_at TEXT
);

CREATE TABLE IF NOT EXISTS operations (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    state TEXT NOT NULL,
    request_id TEXT NOT NULL,
    actor TEXT NOT NULL,
    resource TEXT NOT NULL,
    result_json BLOB,
    problem_json BLOB,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS idempotency (
    actor TEXT NOT NULL,
    scope TEXT NOT NULL,
    key_digest TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    operation_id TEXT NOT NULL REFERENCES operations(id) ON DELETE RESTRICT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TEXT NOT NULL,
    PRIMARY KEY(actor, scope, key_digest)
);

CREATE TABLE IF NOT EXISTS operation_payloads (
    operation_id TEXT PRIMARY KEY REFERENCES operations(id) ON DELETE CASCADE,
    version INTEGER NOT NULL CHECK (version > 0),
    resource TEXT NOT NULL,
    expected_revision INTEGER NOT NULL CHECK (expected_revision > 0),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    digest TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    resource TEXT NOT NULL,
    result TEXT NOT NULL,
    request_id TEXT NOT NULL,
    before_digest TEXT,
    after_digest TEXT
);

CREATE INDEX IF NOT EXISTS audit_events_by_timestamp
    ON audit_events(timestamp);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (3);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (4);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (5);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (6);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (7);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (8);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (9);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (10);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (11);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (12);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (13);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (14);

COMMIT;
`
