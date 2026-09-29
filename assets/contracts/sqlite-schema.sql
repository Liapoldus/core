BEGIN IMMEDIATE;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plugin_instances (
    id TEXT PRIMARY KEY,
    mode TEXT NOT NULL CHECK (mode IN ('local', 'remote')),
    endpoint TEXT,
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

CREATE TABLE IF NOT EXISTS plugin_launch_settings (
    instance_id TEXT PRIMARY KEY REFERENCES plugin_instances(id) ON DELETE CASCADE,
    launch_json BLOB NOT NULL CHECK (json_valid(launch_json))
);

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

COMMIT;
