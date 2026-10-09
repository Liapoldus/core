import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { createGoWorkspace, missingWorkspaceRepositories } from "../support/workspace.js";

const root = resolve(import.meta.dirname, "../..");
const missing = missingWorkspaceRepositories("plugin-sdk");

describe.skipIf(missing.length > 0)("SQLite startup integrity gate", () => {
  it("refuses an orphaned foreign key before Core serves requests", () => {
    const workspace = createGoWorkspace("core", "plugin-sdk");
    let output: string;
    try {
      output = execFileSync("go", ["run", "./tests/fixtures/sqlite-integrity"], {
        cwd: root,
        encoding: "utf8",
        timeout: 30_000,
        env: { ...process.env, GOWORK: workspace.path, GOTOOLCHAIN: "go1.26.0" },
      });
    } finally {
      workspace.cleanup();
    }
    expect(JSON.parse(output)).toEqual({ orphanRejected: true, validReopen: true });
  }, 30_000);
});
