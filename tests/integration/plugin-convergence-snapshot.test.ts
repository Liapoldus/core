import { execFile } from "node:child_process";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const root = join(import.meta.dirname, "../..");

describe("plugin convergence snapshot", () => {
  it("serves desired and observed state from memory after SQLite becomes unavailable", async () => {
    const { stdout } = await execFileAsync("go", ["run", "./tests/fixtures/plugin-convergence-snapshot"], { cwd: root });
    expect(JSON.parse(stdout)).toEqual({
      desiredGeneration: 1,
      observedGeneration: 1,
      observations: 1,
      unavailableAfterInvalidation: true,
    });
  }, 30_000);
});
