import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("plugin configuration SQLite migration", () => {
  it("preserves legacy active, previous and in-flight operation bytes across repeat startup", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-config-migration-"));
    const databasePath = join(directory, "core.db");
    try {
      const result = await execFileAsync("go", ["run", "./tests/fixtures/plugin-config-migration-v5", databasePath], {
        cwd: coreRoot,
      });
      expect(JSON.parse(result.stdout)).toMatchObject({
        migrationVersion: 14,
        active: { generation: 2, raw: '{ "version" : 2 }' },
        previous: { generation: 1, raw: '{"version":1}' },
        staging: { generation: 3, raw: '{ "version" : 3 }' },
        operationState: "pending",
        payloadHasNoSettings: true,
        repeatedStartupPreserved: true,
        legacyTablesRemoved: true,
        legacyTopologyColumnsRemoved: true,
        replicaRowsPreserved: true,
        foreignKeysValid: true,
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
