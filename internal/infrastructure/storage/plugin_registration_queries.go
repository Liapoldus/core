package storage

const (
	registerPluginInstanceSQL = `INSERT OR IGNORE INTO plugin_instances (id, manifest_json, state, updated_at)
VALUES (?, ?, ?, ?);
`
	registerPluginReplicaSQL = `INSERT OR IGNORE INTO plugin_replicas (instance_id, replica_id, observed_generation, observed_state, last_failure_code, observed_at)
VALUES (?, ?, NULL, ?, '', ?);
`
	markPluginInstanceRegisteredSQL = `INSERT OR IGNORE INTO plugin_registered_instances(instance_id, registered_at)
VALUES (?, ?);
`
	listRegisteredPluginInstancesSQL = `SELECT instance_id
FROM plugin_registered_instances
ORDER BY instance_id;
`
)
