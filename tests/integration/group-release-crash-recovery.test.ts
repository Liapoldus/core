import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("group release startup recovery", () => {
  it("restores the SQLite current composition and fails/discards an activated uncommitted release", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-release-recovery-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-release-recovery", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        runtimeBeforeRestart: string;
        runtimeAfterCurrentActivation: string;
        runtimeAfterRecovery: string;
        currentBeforeRecovery: string;
        previousBeforeRecovery: string;
        currentAfterRecovery: string;
        previousAfterRecovery: string;
        operationState: string;
        journalState: string;
        pendingCount: number;
        stagedCaddyfileExists: boolean;
        stagedArtifactExists: boolean;
      };

      expect(report.runtimeBeforeRestart).toBe("candidate-release");
      expect(report.runtimeAfterCurrentActivation).toBe("current-release");
      expect(report.runtimeAfterRecovery).toBe("current-release");
      expect(report.currentAfterRecovery).toBe(report.currentBeforeRecovery);
      expect(report.previousAfterRecovery).toBe(report.previousBeforeRecovery);
      expect(report.operationState).toBe("failed");
      expect(report.journalState).toBe("failed");
      expect(report.pendingCount).toBe(0);
      expect(report.stagedCaddyfileExists).toBe(false);
      expect(report.stagedArtifactExists).toBe(false);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 15_000);
});
