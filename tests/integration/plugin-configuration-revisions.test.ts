import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("durable plugin configuration revisions", () => {
  it("keeps candidates inactive until ACK commit, supports rollback, and restores state after reopen", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-config-revisions-"));
    const database = join(directory, "core.db");
    try {
      const result = await execFileAsync("go", ["run", "./tests/fixtures/plugin-config-revisions", database], {
        cwd: coreRoot,
      });
      expect(JSON.parse(result.stdout)).toEqual({
        initialRevision: 1,
        candidateRevision: 2,
        candidateStayedPending: true,
        activatedCurrent: 2,
        activatedPrevious: 1,
        failedCandidateDidNotChangeCurrent: true,
        failedCandidateRemoved: true,
        staleRevisionRejected: true,
        restoredCurrent: 1,
        restoredPrevious: 2,
        reopenedCurrent: 1,
        reopenedPending: 3,
        reopenedPendingState: "staging",
        reopenedPendingSettings: '{"origin":"pending-after-reopen"}',
        missingRevisionRejected: true,
        digestMatchesDocument: true,
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 30_000);
});
