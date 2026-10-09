package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Liapoldus/core/internal/infrastructure/config"
	settingsstore "github.com/Liapoldus/core/internal/infrastructure/storage/settings"
	bootstrapruntime "github.com/Liapoldus/core/internal/presentation/cli/bootstrap"
)

func TestPrepareMigrationConvertsSettingsAndReportsStaticMembership(t *testing.T) {
	directory := t.TempDir()
	input := writeLegacyBootstrap(t, directory)
	plan, legacy, raw, err := prepareMigration(input)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.StatePath != filepath.Join(directory, "state", "core.sqlite") {
		t.Fatalf("state path was not resolved: %q", legacy.StatePath)
	}
	if plan.Settings.SecretRoot != directory || plan.Settings.Management.MaxBodyBytes != 1<<20 {
		t.Fatalf("settings conversion: %+v", plan.Settings)
	}
	if len(plan.IgnoredStaticPluginInstances) != 1 || plan.IgnoredStaticPluginInstances[0] != "legacy-plugin" {
		t.Fatalf("static registry migration report: %#v", plan.IgnoredStaticPluginInstances)
	}
	decoded, err := config.DecodeSettings(raw)
	if err != nil || decoded.SecretRoot != directory {
		t.Fatalf("converted settings are invalid: %+v (%v)", decoded, err)
	}
}

func TestRunDryRunDoesNotCreateOrChangeSQLite(t *testing.T) {
	directory := t.TempDir()
	input := writeLegacyBootstrap(t, directory)
	statePath := filepath.Join(directory, "state", "core.sqlite")
	var output bytes.Buffer
	if err := run([]string{"--input", input, "--dry-run"}, &output); err != nil {
		t.Fatal(err)
	}
	var plan migrationPlan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
		t.Fatalf("decode dry-run plan: %v", err)
	}
	if plan.SchemaVersion != 1 || plan.Settings.Management.Listen != "127.0.0.1:9443" {
		t.Fatalf("unexpected dry-run plan: %+v", plan)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("dry-run touched SQLite state: %v", err)
	}
	if err := run([]string{"--input", input}, &output); err == nil {
		t.Fatal("migration without an explicit mode was accepted")
	}
}

func TestImportSettingsPreservesExistingSQLiteDataAndGenerations(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "state", "core.sqlite")
	seedLegacyState(t, databasePath, false)
	input := writeLegacyBootstrap(t, directory)
	_, legacy, raw, err := prepareMigration(input)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := importSettings(context.Background(), legacy.StatePath, raw)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("successful import did not retain a pre-migration backup")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup is unavailable: %v", err)
	}
	database := openSQLiteForTest(t, databasePath)
	defer database.Close()
	settings, err := settingsstore.New(database).Read(context.Background())
	if err != nil || settings.DesiredRevision != 1 || settings.EffectiveRevision != 0 {
		t.Fatalf("imported settings revision: %+v (%v)", settings, err)
	}
	if got, err := readRawGeneration(database, 2, "active"); err != nil || string(got) != ` { "form" : "contact", "limit" : 10 } ` {
		t.Fatalf("active plugin settings bytes changed: %q (%v)", got, err)
	}
	if got, err := readRawGeneration(database, 1, "previous"); err != nil || string(got) != `{"form":"legacy"}` {
		t.Fatalf("previous plugin settings bytes changed: %q (%v)", got, err)
	}
	var auditCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE request_id = 'legacy-audit'`).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatalf("audit row not preserved: count=%d err=%v", auditCount, err)
	}
	var productValue string
	if err := database.QueryRow(`SELECT value FROM migration_product_sentinel WHERE id = 'keep'`).Scan(&productValue); err != nil || productValue != "opaque-product-data" {
		t.Fatalf("product data not preserved: value=%q err=%v", productValue, err)
	}
}

func TestImportSettingsRollsBackAndKeepsBackupWhenSettingsAlreadyExist(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "state", "core.sqlite")
	seedLegacyState(t, databasePath, true)
	_, legacy, raw, err := prepareMigration(writeLegacyBootstrap(t, directory))
	if err != nil {
		t.Fatal(err)
	}
	_, err = importSettings(context.Background(), legacy.StatePath, raw)
	if err == nil || !strings.Contains(err.Error(), "pre-migration backup retained at") {
		t.Fatalf("repeated import did not fail with a recoverable backup: %v", err)
	}
	const backupMarker = "pre-migration backup retained at \""
	backupStart := strings.Index(err.Error(), backupMarker)
	if backupStart < 0 {
		t.Fatalf("rollback error omitted its recovery backup: %v", err)
	}
	backupPath := strings.SplitN(err.Error()[backupStart+len(backupMarker):], "\"", 2)[0]
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("automatic rollback did not retain its backup: %v", err)
	}
	database := openSQLiteForTest(t, databasePath)
	defer database.Close()
	settings, err := settingsstore.New(database).Read(context.Background())
	if err != nil || settings.DesiredRevision != 1 || string(settings.DesiredDocument) != `{"secretRoot":"preserve-me"}` {
		t.Fatalf("failed import did not restore existing settings: %+v (%v)", settings, err)
	}
	if got, err := readRawGeneration(database, 2, "active"); err != nil || string(got) != ` { "form" : "contact", "limit" : 10 } ` {
		t.Fatalf("failed import changed plugin data: %q (%v)", got, err)
	}
}

func writeLegacyBootstrap(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy.yaml")
	contents := `state:
  path: ./state/core.sqlite
management:
  listen: 127.0.0.1:9443
  tls:
    certificate: file:/run/secrets/management.crt
    key: file:/run/secrets/management.key
pluginControl:
  listen: 127.0.0.1:9444
  publicURL: https://core.internal:9444
  tls:
    certificate: file:/run/secrets/control.crt
    key: file:/run/secrets/control.key
    replicaClientCA: file:/run/secrets/replica-client-ca.crt
    replicaServerCA: file:/run/secrets/replica-server-ca.crt
plugins:
  - instanceId: legacy-plugin
    replicas:
      - replicaId: legacy-replica
        endpoint: https://plugin.internal:9445
        expectedPeerIdentity:
          commonName: legacy-replica
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func seedLegacyState(t *testing.T, path string, withCoreSettings bool) {
	t.Helper()
	database, err := bootstrapruntime.OpenDatabase(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO plugin_instances(id, manifest_json, state) VALUES ('forms', '{}', 'configured')`); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []struct {
		generation int
		slot       string
		raw        []byte
	}{{1, "previous", []byte(`{"form":"legacy"}`)}, {2, "active", []byte(` { "form" : "contact", "limit" : 10 } `)}} {
		digest := sha256.Sum256(raw.raw)
		if _, err := database.Exec(`INSERT INTO plugin_config_generations(instance_id, generation, slot, raw_json, sha256, schema_version, created_at) VALUES ('forms', ?, ?, ?, ?, 1, '2026-01-01T00:00:00Z')`, raw.generation, raw.slot, raw.raw, hex.EncodeToString(digest[:])); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO audit_events(actor, action, resource, result, request_id) VALUES ('operator', 'legacy-change', 'forms', 'succeeded', 'legacy-audit')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE migration_product_sentinel(id TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO migration_product_sentinel(id, value) VALUES ('keep', 'opaque-product-data')`); err != nil {
		t.Fatal(err)
	}
	if withCoreSettings {
		if err := settingsstore.EnsureSchema(database); err != nil {
			t.Fatal(err)
		}
		if _, err := settingsstore.New(database).Init(context.Background(), []byte(`{"secretRoot":"preserve-me"}`), "existing-init"); err != nil {
			t.Fatal(err)
		}
	}
}

func openSQLiteForTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := bootstrapruntime.OpenDatabase(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func readRawGeneration(database *sql.DB, generation int, slot string) ([]byte, error) {
	var raw []byte
	err := database.QueryRow(`SELECT raw_json FROM plugin_config_generations WHERE instance_id = 'forms' AND generation = ? AND slot = ?`, generation, slot).Scan(&raw)
	return raw, err
}
