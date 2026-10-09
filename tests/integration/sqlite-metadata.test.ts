import { execFile } from "node:child_process";
import { mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("SQLite control-plane state", () => {
  it("opens a local WAL database, applies migrations once and preserves schema after restart", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-sqlite-"));
    const database = join(directory, "state", "core.db");
    try {
      const first = await execFileAsync("go", ["run", "./tests/fixtures/sqlite-probe", database], {
        cwd: coreRoot,
      });
      const firstReport = JSON.parse(first.stdout) as { migrationVersion: number; requiredTables: string[] };
      expect(firstReport).toMatchObject({
        journalMode: "wal",
        foreignKeys: 1,
        migrationVersion: 14,
        requiredTables: expect.arrayContaining(["schema_migrations", "plugin_instances", "plugin_config_generations", "service_keys", "operations", "idempotency", "operation_payloads", "audit_events", "plugin_rollouts", "plugin_rollout_targets", "plugin_link_policies", "traffic_rollouts", "traffic_rollout_stages", "traffic_rollout_cohort_targets", "traffic_rollout_confirmations"]),
      });
      expect(firstReport.requiredTables).toContain("plugin_replicas");
      expect(firstReport.requiredTables).not.toContain("plugin_config_revisions");
      expect(firstReport.requiredTables).not.toContain("plugin_config_pointers");
      // v1 never supervises a plugin process, so the local-launch settings table
      // is dropped rather than carried forward into the v1 schema.
      expect(firstReport.requiredTables).not.toContain("plugin_launch_settings");
      expect((await stat(database)).mode & 0o077).toBe(0);
      expect((await stat(join(directory, "state"))).mode & 0o077).toBe(0);

      const second = await execFileAsync("go", ["run", "./tests/fixtures/sqlite-probe", database], {
        cwd: coreRoot,
      });
      expect(JSON.parse(second.stdout)).toMatchObject({ migrationVersion: 14, migrationCount: 12 });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
