import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

describe("SQLite audit retention", () => {
  it("prunes expired records and returns only the retained audit window", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-sqlite-audit-retention-"));
    const binary = join(directory, "sqlite-audit-store");
    try {
      await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/sqlite-audit-store"], { cwd: coreRoot });
      const { stdout } = await execFileAsync(binary, [join(directory, "gateway.db")], { cwd: coreRoot });
      expect(JSON.parse(stdout)).toEqual({ expiredRemaining: 0, retainedActors: ["recent-actor"] });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
