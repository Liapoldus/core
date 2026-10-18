package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Liapoldus/core/v3/internal/domain/models"
)

func TestSQLiteServiceConfigurationRolloutBlockersDistinguishConfigurationAndTraffic(t *testing.T) {
	ctx := context.Background()
	database, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "core.db"), SQLiteOptions{
		Driver: "sqlite", ParentDirectoryMode: 0o700, DatabaseFileMode: 0o600,
		MaxOpenConnections: 1, MaxIdleConnections: 1, SchemaVersion: 17,
		HasMigrationTableQuery: `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations')`,
		MigrationVersionQuery:  `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`,
		SchemaVersionError:     "schema version mismatch",
		IntegrityCheckQuery:    "PRAGMA quick_check(1)", ForeignKeyCheckQuery: "PRAGMA foreign_key_check",
		IntegritySuccess: "ok", IntegrityError: "integrity check failed",
		Pragmas: "PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA synchronous = FULL; PRAGMA busy_timeout = 5000;",
	}, []byte(SchemaSQL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := RegisterServiceInstance(ctx, database, "server", []byte(`{}`), "ready", "2026-10-18T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	store, err := NewSQLiteServiceConfigurationStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
INSERT INTO operations(id,kind,state,request_id,actor,resource) VALUES
 ('config-op','service.settings.apply','pending','req-config','test','server'),
 ('traffic-op','service.traffic.rollout','failed','req-traffic','test','server');
INSERT INTO service_rollouts(operation_id,instance_id,generation,open,created_at) VALUES
 ('config-op','server',1,1,'2026-10-18T00:00:00Z');
INSERT INTO traffic_rollouts(id,operation_id,instance_id,generation,release_sha256,plan_json,state,active_stage_index,revision,created_at,updated_at)
VALUES ('traffic-id','traffic-op','server',1,?, '{}','failed',0,1,'2026-10-18T00:00:00Z','2026-10-18T00:00:00Z');`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	blockers, err := store.RolloutBlockers(ctx, "server")
	want := []models.ConfigurationBlocker{models.ConfigurationBlockerServiceRollout, models.ConfigurationBlockerTrafficRollout}
	if err != nil || !reflect.DeepEqual(blockers, want) {
		t.Fatalf("rollout blockers = %#v, %v; want %#v", blockers, err, want)
	}
	if _, err := database.ExecContext(ctx, `UPDATE service_rollouts SET open = 0 WHERE operation_id = 'config-op'`); err != nil {
		t.Fatal(err)
	}
	blockers, err = store.RolloutBlockers(ctx, "server")
	if err != nil || !reflect.DeepEqual(blockers, []models.ConfigurationBlocker{models.ConfigurationBlockerTrafficRollout}) {
		t.Fatalf("completed config rollout obscured traffic blocker: %#v %v", blockers, err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE traffic_rollouts SET state = 'completed' WHERE id = 'traffic-id'`); err != nil {
		t.Fatal(err)
	}
	blockers, err = store.RolloutBlockers(ctx, "server")
	if err != nil || len(blockers) != 0 {
		t.Fatalf("completed rollouts still block submission: %#v %v", blockers, err)
	}
}
