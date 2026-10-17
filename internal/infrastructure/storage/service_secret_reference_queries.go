package storage

func serviceSecretReferenceDefinitions() sqliteServiceSecretReferenceStoreContract {
	return sqliteServiceSecretReferenceStoreContract{
		Insert:          "INSERT INTO service_secret_references(reference, service_id, purpose, label, state, created_at) VALUES(?, ?, ?, ?, ?, ?)",
		SelectOne:       "SELECT reference, service_id, purpose, label, state, created_at FROM service_secret_references WHERE service_id = ? AND reference = ?",
		SelectPage:      "SELECT reference, service_id, purpose, label, state, created_at FROM service_secret_references WHERE service_id = ? AND (? = '' OR purpose = ?) ORDER BY created_at, reference",
		Revoke:          "UPDATE service_secret_references SET state = 'revoked' WHERE service_id = ? AND reference = ? AND state = 'active'",
		TimestampLayout: "2006-01-02T15:04:05.999999999Z07:00",
		Diagnostics: struct {
			InvalidContract string
			InvalidStore    string
		}{InvalidContract: "SQLite service secret reference contract is invalid.", InvalidStore: "SQLite service secret reference metadata is invalid."},
	}
}
