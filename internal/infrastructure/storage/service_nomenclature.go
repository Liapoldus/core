package storage

import (
	"context"
	"database/sql"

	settingsstore "github.com/Liapoldus/core/v3/internal/infrastructure/storage/settings"
)

// V15 changes schema names only. Never rewrite manifests, generation bytes,
// operation results/problems or audit history as part of a naming migration.
const serviceNomenclatureV15 = `
ALTER TABLE plugin_instances RENAME TO service_instances;
ALTER TABLE plugin_config_generations RENAME TO service_config_generations;
ALTER TABLE plugin_replicas RENAME TO service_replicas;
ALTER TABLE plugin_registered_instances RENAME TO service_registered_instances;
ALTER TABLE plugin_rollouts RENAME TO service_rollouts;
ALTER TABLE plugin_rollout_targets RENAME TO service_rollout_targets;
ALTER TABLE plugin_rollout_target_leases RENAME TO service_rollout_target_leases;
ALTER TABLE plugin_link_policies RENAME TO service_link_policies;
DROP INDEX one_open_plugin_rollout_per_instance;
CREATE UNIQUE INDEX one_open_service_rollout_per_instance ON service_rollouts(instance_id) WHERE open = 1;
DROP INDEX plugin_config_generations_by_instance;
CREATE INDEX service_config_generations_by_instance ON service_config_generations(instance_id, generation DESC);
UPDATE operations SET kind = CASE kind
    WHEN 'plugin-settings-apply' THEN 'service-settings-apply'
    WHEN 'plugin-settings-rollback' THEN 'service-settings-rollback'
    WHEN 'plugin-link-create' THEN 'service-link-create'
    WHEN 'plugin-link-replace' THEN 'service-link-replace'
    WHEN 'plugin-link-delete' THEN 'service-link-delete'
    ELSE kind END
WHERE state IN ('pending', 'running');
INSERT INTO schema_migrations(version) VALUES (15);
`

func migrateServiceNomenclature(ctx context.Context, database *sql.DB) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, serviceNomenclatureV15); err != nil {
		return err
	}
	if err := settingsstore.MigrateServiceNomenclature(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// V16 binds durable configuration acknowledgements to the exact authenticated
// service process incarnation. Existing observations remain unknown until that
// process acknowledges again; they must never be attributed to a replacement.
const serviceReplicaIncarnationV16 = `
ALTER TABLE service_replicas ADD COLUMN incarnation_id TEXT NOT NULL DEFAULT '';
INSERT INTO schema_migrations(version) VALUES (16);
`

func migrateServiceReplicaIncarnation(ctx context.Context, database *sql.DB) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, serviceReplicaIncarnationV16); err != nil {
		return err
	}
	return tx.Commit()
}
