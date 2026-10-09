import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { createGoWorkspace } from "../support/workspace.js";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

interface Observations {
  initialRules: number;
  afterCreateRules: number;
  createWeight: number;
  otherCallerRules: number;
  afterReplaceCount: number;
  replaceCarrier: string;
  afterDeleteRules: number;
  restartRestoredRules: number;
}

describe("Core peer link policy snapshot", () => {
  it("publishes deny-by-default rules, refreshes on mutation and restores at startup", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-links-snapshot-"));
    const workspace = createGoWorkspace("core", "plugin-sdk");
    try {
      const { stdout } = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/link-policy-snapshot", join(directory, "core.db")],
        { cwd: coreRoot, env: { ...process.env, GOWORK: workspace.path, GOTOOLCHAIN: "go1.26.0" }, timeout: 120_000 },
      );
      const observed = JSON.parse(stdout) as Observations;

      expect(observed.initialRules).toBe(0);
      expect(observed.afterCreateRules).toBe(1);
      expect(observed.createWeight).toBe(5);
      expect(observed.otherCallerRules).toBe(0);
      expect(observed.afterReplaceCount).toBe(1);
      expect(observed.replaceCarrier).toBe("unix");
      expect(observed.afterDeleteRules).toBe(0);
      expect(observed.restartRestoredRules).toBe(1);
    } finally {
      workspace.cleanup();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});