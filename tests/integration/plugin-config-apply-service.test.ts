import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("plugin configuration apply workflow", () => {
 	 it("keeps the desired generation active when Reload is rejected", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-config-apply-"));
    try {
      const result = await execFileAsync("go", ["run", "./tests/fixtures/plugin-config-apply-service", join(directory, "core.db")], { cwd: coreRoot });
      expect(JSON.parse(result.stdout)).toMatchObject({
        appliedRevision: 2,
        activeAfterApply: 2,
        previousAfterApply: 1,
        acknowledgedConfig: '{"origin":"new"}',
        failedRevision: 3,
        activeAfterReject: 3,
        failedCandidateRemoved: false,
        staleRevisionRejected: true,
        activeConfig: '{"origin":"rejected"}',
        cancelledApplyReturnedError: true,
        cancelledCandidateRemoved: false,
        currentAfterCancelledApply: 4,
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 30_000);
});
