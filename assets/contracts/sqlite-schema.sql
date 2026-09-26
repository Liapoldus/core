BEGIN IMMEDIATE;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS groups (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('system', 'application')),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TEXT
);

CREATE TABLE IF NOT EXISTS group_revisions (
    id TEXT PRIMARY KEY,
    group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    caddyfile_digest TEXT NOT NULL,
    artifact_digest TEXT,
    caddyfile_path TEXT NOT NULL,
    artifact_path TEXT,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    actor TEXT NOT NULL,
    UNIQUE(group_id, id)
);

CREATE INDEX IF NOT EXISTS group_revisions_by_group
    ON group_revisions(group_id, created_at DESC, id);

CREATE TABLE IF NOT EXISTS group_release_journal (
    operation_id TEXT PRIMARY KEY REFERENCES operations(id) ON DELETE RESTRICT,
    group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    expected_current_revision_id TEXT,
    revision_id TEXT NOT NULL,
    caddyfile_digest TEXT NOT NULL,
    artifact_digest TEXT,
    caddyfile_path TEXT NOT NULL,
    artifact_path TEXT,
    actor TEXT NOT NULL,
    request_id TEXT NOT NULL,
    state TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS group_release_journal_by_state
    ON group_release_journal(state, created_at);

CREATE TABLE IF NOT EXISTS group_pointers (
    group_id TEXT PRIMARY KEY REFERENCES groups(id) ON DELETE RESTRICT,
    current_revision_id TEXT,
    previous_revision_id TEXT,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(group_id, current_revision_id) REFERENCES group_revisions(group_id, id) ON DELETE RESTRICT,
    FOREIGN KEY(group_id, previous_revision_id) REFERENCES group_revisions(group_id, id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS plugin_instances (
    id TEXT PRIMARY KEY,
    mode TEXT NOT NULL CHECK (mode IN ('local', 'remote')),
    endpoint TEXT,
    settings_json BLOB NOT NULL,
    manifest_json BLOB NOT NULL,
    state TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plugin_launch_settings (
    instance_id TEXT PRIMARY KEY REFERENCES plugin_instances(id) ON DELETE CASCADE,
    launch_json BLOB NOT NULL CHECK (json_valid(launch_json))
);

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

CREATE TABLE IF NOT EXISTS caddy_checkpoints (
    id TEXT PRIMARY KEY,
    runtime_digest TEXT NOT NULL,
    snapshot_path TEXT NOT NULL,
    operation_id TEXT REFERENCES operations(id) ON DELETE RESTRICT,
    actor TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (2);

INSERT OR IGNORE INTO groups(id, kind, active)
VALUES ('system', 'system', 1);

INSERT OR IGNORE INTO group_pointers(group_id)
VALUES ('system');

COMMIT;
