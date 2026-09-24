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
    const database = join(directory, "state", "gateway.db");
    try {
      const first = await execFileAsync("go", ["run", "./tests/fixtures/sqlite-probe", database], {
        cwd: coreRoot,
      });
      expect(JSON.parse(first.stdout)).toMatchObject({
        journalMode: "wal",
        foreignKeys: 1,
        migrationVersion: 1,
        systemGroupExists: true,
        systemPointerExists: true,
        crossGroupPointerRejected: true,
        requiredTables: expect.arrayContaining(["schema_migrations", "groups", "group_revisions", "group_pointers", "plugin_instances", "service_keys", "operations", "idempotency", "audit_events", "caddy_checkpoints"]),
      });
      expect((await stat(database)).mode & 0o077).toBe(0);
      expect((await stat(join(directory, "state"))).mode & 0o077).toBe(0);

      const second = await execFileAsync("go", ["run", "./tests/fixtures/sqlite-probe", database], {
        cwd: coreRoot,
      });
      expect(JSON.parse(second.stdout)).toMatchObject({ migrationVersion: 1, migrationCount: 1 });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
