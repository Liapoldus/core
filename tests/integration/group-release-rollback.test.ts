import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("group release rollback", () => {
  it("reactivates previous revision and atomically swaps current and previous pointers", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-rollback-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-management-api", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        rollbackStatus: number;
        rollbackCode: string;
        currentAfterRollback: string | null;
        previousAfterRollback: string | null;
      };

      expect(report.rollbackStatus).toBe(202);
      expect(report.rollbackCode).toBe("");
      expect(report.currentAfterRollback).toBe("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
      expect(report.previousAfterRollback).not.toBe("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 15_000);
});
