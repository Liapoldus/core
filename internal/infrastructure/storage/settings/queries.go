package settings

// These tables are independent of the existing storage migration sequence.
// Revisions and audit entries are append-only; only the singleton pointer moves.
var schema = [...]string{
	`CREATE TABLE IF NOT EXISTS core_settings_revisions (
		revision INTEGER PRIMARY KEY CHECK (revision > 0),
		document BLOB NOT NULL CHECK (typeof(document) = 'blob' AND json_valid(document)),
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS core_settings_state (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		desired_revision INTEGER NOT NULL REFERENCES core_settings_revisions(revision),
		effective_revision INTEGER REFERENCES core_settings_revisions(revision),
		CHECK (effective_revision IS NULL OR effective_revision <= desired_revision)
	)`,
	`CREATE TABLE IF NOT EXISTS core_settings_audit (
		sequence INTEGER PRIMARY KEY,
		actor TEXT NOT NULL,
		operation TEXT NOT NULL CHECK (operation IN ('init', 'update', 'activate', 'recover')),
		previous_desired_revision INTEGER NOT NULL,
		previous_effective_revision INTEGER NOT NULL,
		desired_revision INTEGER NOT NULL REFERENCES core_settings_revisions(revision),
		effective_revision INTEGER NOT NULL,
		source_revision INTEGER REFERENCES core_settings_revisions(revision),
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TRIGGER IF NOT EXISTS core_settings_revisions_no_update
	 BEFORE UPDATE ON core_settings_revisions BEGIN SELECT RAISE(ABORT, 'immutable revision'); END`,
	`CREATE TRIGGER IF NOT EXISTS core_settings_revisions_no_delete
	 BEFORE DELETE ON core_settings_revisions BEGIN SELECT RAISE(ABORT, 'immutable revision'); END`,
	`CREATE TRIGGER IF NOT EXISTS core_settings_audit_no_update
	 BEFORE UPDATE ON core_settings_audit BEGIN SELECT RAISE(ABORT, 'immutable audit'); END`,
	`CREATE TRIGGER IF NOT EXISTS core_settings_audit_no_delete
	 BEFORE DELETE ON core_settings_audit BEGIN SELECT RAISE(ABORT, 'immutable audit'); END`,
}

const (
	// Acquire the SQLite writer lock before reading the CAS pointers. This also
	// works on a fresh database, without inserting an uninitialized state row.
	lockState = `UPDATE core_settings_state SET desired_revision = desired_revision WHERE id = 1`
	readState = `SELECT s.desired_revision, COALESCE(s.effective_revision, 0), d.document, e.document
	 FROM core_settings_state s JOIN core_settings_revisions d ON d.revision = s.desired_revision
	 LEFT JOIN core_settings_revisions e ON e.revision = s.effective_revision WHERE s.id = 1`
	insertRevision  = `INSERT INTO core_settings_revisions (revision, document) VALUES (?, ?)`
	insertState     = `INSERT INTO core_settings_state (id, desired_revision) VALUES (1, 1)`
	updateDesired   = `UPDATE core_settings_state SET desired_revision = ? WHERE id = 1 AND desired_revision = ?`
	updateEffective = `UPDATE core_settings_state SET effective_revision = ? WHERE id = 1 AND desired_revision = ?`
	readRevision    = `SELECT document FROM core_settings_revisions WHERE revision = ?`
	insertAudit     = `INSERT INTO core_settings_audit
	 (actor, operation, previous_desired_revision, previous_effective_revision,
	 desired_revision, effective_revision, source_revision) VALUES (?, ?, ?, ?, ?, ?, ?)`
)
