package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Operational settings revisions are immutable too. Append renamed documents
// and move pointers; never overwrite a historical revision or audit entry.
func MigrateServiceNomenclature(ctx context.Context, tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='core_settings_state' AND type='table')`).Scan(&exists); err != nil || !exists {
		return err
	}
	var desired, effective int64
	if err := tx.QueryRowContext(ctx, `SELECT desired_revision, COALESCE(effective_revision,0) FROM core_settings_state WHERE id=1`).Scan(&desired, &effective); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	newRevisions := map[int64]int64{}
	for _, source := range []int64{effective, desired} {
		if source == 0 {
			continue
		}
		if _, done := newRevisions[source]; done {
			continue
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT document FROM core_settings_revisions WHERE revision=?`, source).Scan(&raw); err != nil {
			return err
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(raw, &document); err != nil {
			return err
		}
		control, retired := document["pluginControl"]
		if !retired && (source != desired || newRevisions[effective] <= source) {
			newRevisions[source] = source
			continue
		}
		updated := raw
		if retired {
			if _, conflict := document["serviceControl"]; conflict {
				return errors.New("operational settings nomenclature conflict")
			}
			document["serviceControl"] = control
			delete(document, "pluginControl")
			var err error
			updated, err = json.Marshal(document)
			if err != nil {
				return err
			}
		}
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0)+1 FROM core_settings_revisions`).Scan(&revision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_settings_revisions(revision,document) VALUES (?,?)`, revision, updated); err != nil {
			return err
		}
		newRevisions[source] = revision
	}
	newDesired, newEffective := newRevisions[desired], newRevisions[effective]
	if newDesired == desired && newEffective == effective {
		return nil
	}
	var effectivePointer any
	if newEffective > 0 {
		effectivePointer = newEffective
	}
	if _, err := tx.ExecContext(ctx, `UPDATE core_settings_state SET desired_revision=?,effective_revision=? WHERE id=1`, newDesired, effectivePointer); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO core_settings_audit(actor,operation,previous_desired_revision,previous_effective_revision,desired_revision,effective_revision,source_revision) VALUES ('nomenclature-migration-v15','recover',?,?,?,?,?)`, desired, effective, newDesired, newEffective, desired)
	return err
}
