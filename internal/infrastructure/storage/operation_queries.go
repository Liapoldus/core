package storage

// operationQueries returns a private set of parameterized statements.
func operationQueries() map[string]string {
	return map[string]string{
		"insert-operation": `INSERT INTO operations(id, kind, state, request_id, actor, resource, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?);`,
		"select-operation": `SELECT id, kind, state, created_at, updated_at, request_id, actor, resource, problem_json
FROM operations WHERE id = ?;`,
		"list-recoverable": `SELECT id, result_json FROM operations WHERE kind = ? AND state IN (?, ?) ORDER BY created_at, id;`,
		"transition-operation": `UPDATE operations SET state = ?, updated_at = ?, problem_json = ?
WHERE id = ? AND state = ?;`,
		"select-idempotency": `SELECT request_digest, operation_id FROM idempotency
WHERE actor = ? AND scope = ? AND key_digest = ? AND expires_at > ?;`,
		"insert-idempotency": `INSERT INTO idempotency(actor, scope, key_digest, request_digest, operation_id, created_at, expires_at)
VALUES(?, ?, ?, ?, ?, ?, ?);`,
		"delete-expired-idempotency": `DELETE FROM idempotency WHERE actor = ? AND scope = ? AND key_digest = ? AND expires_at <= ?;`,
		"insert-operation-payload": `INSERT INTO operation_payloads(operation_id, version, resource, expected_revision, schema_version, digest)
VALUES(?, ?, ?, ?, ?, ?);`,
		"select-operation-payload": `SELECT version, resource, expected_revision, schema_version, digest
FROM operation_payloads WHERE operation_id = ?;`,
	}
}
