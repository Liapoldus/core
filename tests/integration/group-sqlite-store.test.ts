import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("durable group revision pointers", () => {
  it("persists immutable revisions and atomically advances current/previous with CAS", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-store-"));
    try {
      const result = await execFileAsync("go", ["run", "./tests/fixtures/group-sqlite-store", join(directory, "gateway.db")], {
        cwd: coreRoot,
      });
      expect(JSON.parse(result.stdout)).toEqual({
        firstCurrent: "revision-one",
        secondCurrent: "revision-two",
        secondPrevious: "revision-one",
        staleCompareAndSwapRejected: true,
        staleCompareAndSwapUnchangedPointers: true,
        crossGroupRevisionRejected: true,
        missingRevisionRejected: true,
        reopenedCurrent: "revision-two",
        reopenedPrevious: "revision-one",
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
