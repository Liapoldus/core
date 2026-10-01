import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { missingWorkspaceRepositories } from "../support/workspace.js";

const root = resolve(import.meta.dirname, "../..");

// The fixture builds and starts the real Server and forms-db binaries, which
// their own modules resolve from these siblings. The plugin-agnostic gate does
// not check them out, so there the scenario is out of scope rather than failing;
// the integration workflow checks out the full workspace and runs it.
const missing = missingWorkspaceRepositories(
  "pluginprotocol",
  "plugin-sdk",
  "plugins/server",
  "plugins/forms-db",
);

describe.skipIf(missing.length > 0)("manually launched Core, Server and forms-db", () => {
  it("publishes exact settings to both plugins and changes real Server HTTP traffic", () => {
    const output = execFileSync("go", ["run", "./tests/fixtures/manual-core-server"], {
      cwd: root,
      encoding: "utf8",
      timeout: 120_000,
      env: { ...process.env, GOWORK: "off", GOTOOLCHAIN: "go1.26.0" },
    });
    expect(JSON.parse(output)).toEqual({
      first: "old", second: "new", afterRollback: "old",
      active: 1, previous: 2, exactDigest: true,
      formsActive: 1,
      restartedServer: "old",
    });
  }, 120_000);
});
