package storage

func accessDefinitions() sqliteAccessContract {
	return sqliteAccessContract{BeginImmediate: "BEGIN IMMEDIATE",
		Commit:                "COMMIT",
		Rollback:              "ROLLBACK",
		SelectActiveKey:       "SELECT EXISTS(SELECT 1 FROM service_keys WHERE revoked_at IS NULL)",
		InsertKey:             "INSERT INTO service_keys(id, name, verifier, role) VALUES(?, ?, ?, ?)",
		SelectActiveVerifiers: "SELECT id, verifier FROM service_keys WHERE revoked_at IS NULL ORDER BY id",
		SelectKeyMetadata:     "SELECT id, name, role, created_at, expires_at, revoked_at FROM service_keys",
		BootstrapName:         "local-bootstrap",
		InvalidContract:       "SQLite service-key contract is invalid.",
		ActiveKeyConflict:     "an active service key already exists"}
}

func auditDefinitions() sqliteAuditStoreContract {
	return sqliteAuditStoreContract{InsertEvent: "INSERT INTO audit_events(timestamp, actor, action, resource, result, request_id, before_digest, after_digest) VALUES(?, ?, ?, ?, ?, ?, ?, ?)",
		SelectFirstPage:   "SELECT sequence, timestamp, actor, action, resource, result, request_id, before_digest, after_digest FROM audit_events WHERE timestamp >= ? ORDER BY sequence DESC LIMIT ?",
		SelectAfterCursor: "SELECT sequence, timestamp, actor, action, resource, result, request_id, before_digest, after_digest FROM audit_events WHERE timestamp >= ? AND sequence < ? ORDER BY sequence DESC LIMIT ?",
		DeleteExpired:     "DELETE FROM audit_events WHERE timestamp < ?",
		TimestampLayout:   "2006-01-02T15:04:05.000000000Z07:00",
		InvalidContract:   "SQLite audit store contract is invalid.",
		InvalidCursor:     "Audit cursor is invalid.",
		InvalidLimit:      "Audit page limit is invalid.",
		AppendFailure:     "Audit event could not be persisted."}
}

// ConfigurationDefinitions supplies the shared immutable configuration policy.
func ConfigurationDefinitions() pluginConfigurationStoreContract {
	return pluginConfigurationStoreContract{SchemaVersion: 1,
		MaximumPayloadSize: 262144,
		TimestampLayout:    "2006-01-02T15:04:05.999999999Z07:00",
		MigrationActor:     "migration",
		Slots: struct {
			Active   string
			Previous string
			Staging  string
		}{Active: "active",
			Previous: "previous",
			Staging:  "staging"},
		OperationStates: struct {
			Pending string
			Running string
		}{Pending: "pending",
			Running: "running"},
		Diagnostics: struct {
			InvalidContract string
			InvalidDocument string
			InvalidStore    string
			MigrationFailed string
		}{InvalidContract: "plugin configuration contract is invalid",
			InvalidDocument: "plugin configuration document is invalid",
			InvalidStore:    "plugin configuration store is invalid",
			MigrationFailed: "plugin configuration migration failed; stored data was not changed"}}
}

func linkPolicyDefinitions() pluginLinkPolicyStoreContract {
	return pluginLinkPolicyStoreContract{InsertPolicy: "INSERT INTO plugin_link_policies(caller_instance_id, target_instance_id, revision, rules_json, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?)",
		SelectPolicy:       "SELECT revision, rules_json FROM plugin_link_policies WHERE caller_instance_id = ? AND target_instance_id = ?",
		SelectPolicyExists: "SELECT EXISTS(SELECT 1 FROM plugin_link_policies WHERE caller_instance_id = ? AND target_instance_id = ?)",
		SelectAllPolicies:  "SELECT caller_instance_id, target_instance_id, revision, rules_json FROM plugin_link_policies ORDER BY caller_instance_id, target_instance_id",
		UpdatePolicy:       "UPDATE plugin_link_policies SET revision = ?, rules_json = ?, updated_at = ? WHERE caller_instance_id = ? AND target_instance_id = ? AND revision = ?",
		DeletePolicy:       "DELETE FROM plugin_link_policies WHERE caller_instance_id = ? AND target_instance_id = ? AND revision = ?",
		TimestampLayout:    "2006-01-02T15:04:05.000000000Z07:00",
		Diagnostics: struct {
			InvalidContract string
			InvalidStore    string
		}{InvalidContract: "peer link policy store contract is invalid",
			InvalidStore: "peer link policy store is invalid"}}
}

func operationDefinitions() sqliteOperationStoreContract {
	return sqliteOperationStoreContract{TimestampLayout: "2006-01-02T15:04:05.999999999Z07:00",
		ErrorCodeField:       "errorCode",
		IdempotencyExpiresAt: "9999-12-31T23:59:59.999999999Z",
		OperationNotFound:    "The requested operation does not exist.",
		InvalidContract:      "The SQLite operation-store contract is invalid.",
		InvalidReservation:   "The operation reservation is invalid.",
		TransitionConflict:   "The operation state changed before the requested transition.",
		IdempotencyConflict:  "The idempotency key was already used for a different request."}
}
