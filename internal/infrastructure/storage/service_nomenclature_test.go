package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	settingsstore "github.com/Liapoldus/core/v3/internal/infrastructure/storage/settings"
)

func TestServiceNomenclatureMigrationPreservesHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "core.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(SchemaSQL); err != nil {
		t.Fatal(err)
	}
	if err = settingsstore.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	settingsRaw := []byte(`{ "pluginControl": {"listen":"127.0.0.1:8081"}, "neutral": "plugin-id" }`)
	if _, err = settingsstore.New(db).Init(ctx, settingsRaw, "original-actor"); err != nil {
		t.Fatal(err)
	}
	raw := []byte("{ \"plugin\" : \"neutral-id\", \"method\":\"Plugin.Execute\" }\n")
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if _, err = db.Exec(`INSERT INTO plugin_instances(id, manifest_json, state) VALUES ('neutral-id', '{}', 'ready');
INSERT INTO operations(id, kind, state, request_id, actor, resource, result_json, problem_json) VALUES ('history', 'plugin.settings.update', 'completed', 'req', 'actor', '/api/plugins/neutral-id', ?, ?);
INSERT INTO audit_events(actor, action, resource, result, request_id) VALUES ('actor', 'plugin.settings.update', '/api/plugins/neutral-id', 'success', 'req');`, raw, raw); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO operations(id,kind,state,request_id,actor,resource) VALUES ('in-flight','plugin-settings-apply','pending','req','actor','neutral-id')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO plugin_replicas VALUES ('neutral-id','neutral-replica',1,'acknowledged',NULL,'original-time');
INSERT INTO plugin_registered_instances(instance_id) VALUES ('neutral-id');
INSERT INTO plugin_rollouts VALUES ('in-flight','neutral-id',1,1,'original-time',NULL);
INSERT INTO plugin_rollout_targets VALUES ('in-flight','neutral-replica','neutral-incarnation',?,1);
INSERT INTO plugin_rollout_target_leases VALUES ('in-flight','neutral-replica','original-expiry');
INSERT INTO plugin_link_policies VALUES ('neutral-id','neutral-target',7,'[{"method":"Plugin.Execute"}]','original-time','original-time');`, digest); err != nil {
		t.Fatal(err)
	}
	for i, slot := range []string{"active", "previous", "staging"} {
		if _, err = db.Exec(`INSERT INTO plugin_config_generations(instance_id, generation, slot, raw_json, sha256, schema_version, created_at) VALUES ('neutral-id', ?, ?, ?, ?, 1, '2026-10-13T00:00:00Z')`, i+1, slot, raw, digest); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	options := SQLiteOptions{Driver: "sqlite", ParentDirectoryMode: 0700, DatabaseFileMode: 0600, MaxOpenConnections: 1, MaxIdleConnections: 1, SchemaVersion: 16,
		HasMigrationTableQuery: `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='schema_migrations')`, MigrationVersionQuery: `SELECT MAX(version) FROM schema_migrations`, SchemaVersionError: "version",
		Pragmas: `PRAGMA foreign_keys=ON;`, IntegrityCheckQuery: `PRAGMA quick_check`, ForeignKeyCheckQuery: `PRAGMA foreign_key_check`, IntegritySuccess: "ok", IntegrityError: "integrity"}
	for range 2 {
		db, err = OpenSQLite(ctx, path, options, []byte(SchemaSQL))
		if err != nil {
			t.Fatal(err)
		}
		var retired int
		if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'plugin_%' OR name LIKE '%plugin_rollout%'`).Scan(&retired); err != nil || retired != 0 {
			t.Fatalf("retired schema: %d %v", retired, err)
		}
		for i := range 3 {
			var got []byte
			var gotDigest string
			if err = db.QueryRow(`SELECT raw_json, sha256 FROM service_config_generations WHERE generation=?`, i+1).Scan(&got, &gotDigest); err != nil || !bytes.Equal(got, raw) || gotDigest != digest {
				t.Fatalf("generation changed: %v", err)
			}
		}
		var migratedIncarnation string
		if err = db.QueryRow(`SELECT incarnation_id FROM service_replicas WHERE instance_id='neutral-id' AND replica_id='neutral-replica'`).Scan(&migratedIncarnation); err != nil || migratedIncarnation != "" {
			t.Fatalf("legacy replica observation incarnation=%q err=%v", migratedIncarnation, err)
		}
		var resource, kind string
		for _, table := range []string{"service_replicas", "service_registered_instances", "service_rollouts", "service_rollout_targets", "service_rollout_target_leases", "service_link_policies"} {
			var count int
			if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 1 {
				t.Fatalf("preserved %s: %d %v", table, count, err)
			}
		}
		var rules []byte
		var policyRevision int
		if err = db.QueryRow(`SELECT revision,rules_json FROM service_link_policies`).Scan(&policyRevision, &rules); err != nil || policyRevision != 7 || string(rules) != `[{"method":"Plugin.Execute"}]` {
			t.Fatalf("policy changed: %v", err)
		}
		if err = db.QueryRow(`SELECT kind FROM operations WHERE id='in-flight'`).Scan(&kind); err != nil || kind != "service-settings-apply" {
			t.Fatalf("in-flight recovery kind: %s %v", kind, err)
		}
		var historicalSettings []byte
		if err = db.QueryRow(`SELECT document FROM core_settings_revisions WHERE revision=1`).Scan(&historicalSettings); err != nil || !bytes.Equal(historicalSettings, settingsRaw) {
			t.Fatalf("settings history changed: %v", err)
		}
		var currentSettings []byte
		if err = db.QueryRow(`SELECT document FROM core_settings_revisions JOIN core_settings_state ON revision=desired_revision`).Scan(&currentSettings); err != nil || bytes.Contains(currentSettings, []byte(`"pluginControl"`)) || !bytes.Contains(currentSettings, []byte(`"serviceControl"`)) || !bytes.Contains(currentSettings, []byte(`"plugin-id"`)) {
			t.Fatalf("settings migration: %s %v", currentSettings, err)
		}
		var result, problem []byte
		if err = db.QueryRow(`SELECT resource, kind, result_json, problem_json FROM operations WHERE id='history'`).Scan(&resource, &kind, &result, &problem); err != nil || resource != "/api/plugins/neutral-id" || kind != "plugin.settings.update" || !bytes.Equal(result, raw) || !bytes.Equal(problem, raw) {
			t.Fatalf("historical operation changed: %v", err)
		}
		if err = db.QueryRow(`SELECT resource, action FROM audit_events`).Scan(&resource, &kind); err != nil || resource != "/api/plugins/neutral-id" || kind != "plugin.settings.update" {
			t.Fatalf("audit changed: %v", err)
		}
		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	db, err = OpenSQLite(ctx, path, options, []byte(SchemaSQL))
	if err != nil {
		t.Fatal(err)
	}
	store := NewServiceReplicaStore(db, "invalid observation")
	if err := store.Record(ctx, ServiceReplicaObservation{
		InstanceID: "neutral-id", ReplicaID: "neutral-replica", IncarnationID: "current-incarnation",
		ObservedState: ReplicaObservedAcknowledged, ObservedGeneration: sql.NullInt64{Int64: 1, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	observations, err := store.List(ctx)
	if err != nil || len(observations) != 1 || observations[0].IncarnationID != "current-incarnation" {
		t.Fatalf("replica observation did not preserve incarnation: %+v err=%v", observations, err)
	}
	operationStore, err := NewSQLiteOperationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := operationStore.ReplicaOutcomes(ctx, "in-flight", "running", "")
	if err != nil || len(outcomes) != 1 || outcomes[0].ReplicaID != "neutral-replica" || outcomes[0].DesiredGeneration != 1 || outcomes[0].State != "applied" {
		t.Fatalf("applied replica outcome: %+v %v", outcomes, err)
	}
	if _, err = db.Exec(`UPDATE service_rollout_targets SET acknowledged=0 WHERE operation_id='in-flight'`); err != nil {
		t.Fatal(err)
	}
	outcomes, err = operationStore.ReplicaOutcomes(ctx, "in-flight", "failed", "target_lost")
	if err != nil || len(outcomes) != 1 || outcomes[0].State != "failed" || outcomes[0].FailureCode != "target_lost" {
		t.Fatalf("failed replica outcome: %+v %v", outcomes, err)
	}
	outcomes, err = operationStore.ReplicaOutcomes(ctx, "in-flight", "failed", "unsafe failure detail")
	if err != nil || len(outcomes) != 1 || outcomes[0].FailureCode != "" {
		t.Fatalf("unsafe failure code leaked: %+v %v", outcomes, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceNomenclatureOperationalPointersAndAtomicFailure(t *testing.T) {
	for _, mode := range []string{"desired-and-effective", "mixed-desired-and-effective", "conflicting-keys"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "core.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			if _, err = db.Exec(SchemaSQL); err != nil {
				t.Fatal(err)
			}
			if err = settingsstore.EnsureSchema(db); err != nil {
				t.Fatal(err)
			}
			store := settingsstore.New(db)
			original := []byte(`{ "pluginControl": {"listen":"effective"} }`)
			if _, err = store.Init(ctx, original, "original"); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Activate(ctx, 1, func([]byte) error { return nil }); err != nil {
				t.Fatal(err)
			}
			desired := []byte(`{ "pluginControl": {"listen":"desired"} }`)
			if mode == "mixed-desired-and-effective" {
				desired = []byte(`{ "serviceControl": {"listen":"desired"} }`)
			}
			if mode == "conflicting-keys" {
				desired = []byte(`{"pluginControl":{},"serviceControl":{}}`)
			}
			if _, err = store.Update(ctx, 1, desired, "original"); err != nil {
				t.Fatal(err)
			}
			err = migrateServiceNomenclature(ctx, db)
			if mode == "conflicting-keys" {
				if err == nil {
					t.Fatal("conflict accepted")
				}
				var version, count int
				if err = db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 14 {
					t.Fatalf("version changed: %d %v", version, err)
				}
				if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='service_instances'`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial schema: %d %v", count, err)
				}
				if err = db.QueryRow(`SELECT COUNT(*) FROM core_settings_revisions`).Scan(&count); err != nil || count != 2 {
					t.Fatalf("partial revisions: %d %v", count, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := store.Read(ctx)
			if err != nil || snapshot.DesiredRevision <= snapshot.EffectiveRevision || !bytes.Contains(snapshot.DesiredDocument, []byte(`"desired"`)) || !bytes.Contains(snapshot.EffectiveDocument, []byte(`"effective"`)) {
				t.Fatalf("pointer semantics: %+v %v", snapshot, err)
			}
			for i, want := range [][]byte{original, desired} {
				var got []byte
				if err = db.QueryRow(`SELECT document FROM core_settings_revisions WHERE revision=?`, i+1).Scan(&got); err != nil || !bytes.Equal(got, want) {
					t.Fatalf("history %d changed: %v", i+1, err)
				}
			}
		})
	}
}
