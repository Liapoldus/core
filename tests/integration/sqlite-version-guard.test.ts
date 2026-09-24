import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("SQLite schema compatibility", () => {
  it("rejects a database with a migration newer than this Gateway supports", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-sqlite-version-"));
    try {
      const database = join(directory, "gateway.db");
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/sqlite-version-probe", database],
        { cwd: coreRoot },
      );

      expect(JSON.parse(result.stdout)).toEqual({ rejected: true, contractError: true });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
