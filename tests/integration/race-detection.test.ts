import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

// halt_on_error makes the race detector abort the process on the first detected
// race instead of printing a warning and continuing, so a passing run is
// evidence rather than a warning nobody read.
const raceEnvironment = { ...process.env, GORACE: "halt_on_error=1" };

describe("race detection", () => {
  it("applies and reads plugin configurations concurrently without a data race", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-race-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "-race", "./tests/fixtures/plugin-config-concurrency", join(directory, "core.db")],
        { cwd: coreRoot, timeout: 180_000, env: raceEnvironment },
      );
      expect(result.stderr).not.toContain("DATA RACE");
      expect(JSON.parse(result.stdout)).toEqual({
        instances: 8,
        rounds: 6,
        appliedOperations: 48,
        succeededOperations: 48,
        concurrentRequestsObserved: true,
        recoveryAfterSettleWasNoOp: true,
        instancesLeftFenced: 0,
        stagingLeftBehind: 0,
        instancesWithExactlyOneActive: 8,
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 240_000);
});
