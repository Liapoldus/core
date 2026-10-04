INSERT OR IGNORE INTO plugin_replicas (instance_id, replica_id, observed_generation, observed_state, last_failure_code, observed_at)
VALUES (?, ?, NULL, ?, '', ?);
