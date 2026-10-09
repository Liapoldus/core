import { execFile } from "node:child_process";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { createGoWorkspace } from "../support/workspace.js";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

describe("Core plugin peer directory poll endpoint", () => {
  it("authenticates the caller, scopes the directory and wakes pending long-polls", async () => {
    const workspace = createGoWorkspace("core", "plugin-sdk");
    try {
      const { stdout } = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-peer-directory"],
        { cwd: coreRoot, env: { ...process.env, GOWORK: workspace.path, GOTOOLCHAIN: "go1.26.0" }, timeout: 120_000 },
      );
      const observed = JSON.parse(stdout) as Record<string, boolean>;

      expect(Object.values(observed).every((value) => value === true)).toBe(true);
      expect(Object.keys(observed)).toHaveLength(20);
    } finally {
      workspace.cleanup();
    }
  });
});