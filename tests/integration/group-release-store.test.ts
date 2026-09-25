import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("durable group release coordination storage", () => {
  it("deduplicates requests, commits revision pointers and operation atomically, and reopens", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-release-store-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-release-store", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );

      expect(JSON.parse(result.stdout)).toEqual({
        acceptedOperation: "operation-one",
        firstWasDuplicate: false,
        retriedOperation: "operation-one",
        retryWasDuplicate: true,
        idempotencyConflict: true,
        committedCurrent: "revision-next",
        reopenedCurrent: "revision-next",
        reopenedOperationState: "succeeded",
        pendingCountAfterCommit: 0,
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
