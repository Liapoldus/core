package storage

const (
	registerServiceInstanceSQL = `INSERT OR IGNORE INTO service_instances (id, manifest_json, state, updated_at)
VALUES (?, ?, ?, ?);
`
	registerServiceReplicaSQL = `INSERT OR IGNORE INTO service_replicas (instance_id, replica_id, observed_generation, observed_state, last_failure_code, observed_at)
VALUES (?, ?, NULL, ?, '', ?);
`
	markServiceInstanceRegisteredSQL = `INSERT OR IGNORE INTO service_registered_instances(instance_id, registered_at)
VALUES (?, ?);
`
	listRegisteredServiceInstancesSQL = `SELECT instance_id
FROM service_registered_instances
ORDER BY instance_id;
`
)
