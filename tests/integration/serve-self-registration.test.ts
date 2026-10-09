import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { createGoWorkspace, missingWorkspaceRepositories } from "../support/workspace.js";

const root = resolve(import.meta.dirname, "../..");

// The fixture compiles Core from the local module and registers a replica over
// the real mTLS REST lifecycle, so it needs the local Plugin SDK workspace.
const missing = missingWorkspaceRepositories("plugin-sdk");

describe.skipIf(missing.length > 0)("self-registration against a real core serve", () => {
  it("survives restart with a durable marker and requires a fresh registration", () => {
    const workspace = createGoWorkspace("core", "plugin-sdk");
    let output: string;
    try {
      output = execFileSync("go", ["run", "./tests/fixtures/serve-self-registration"], {
        cwd: root,
        encoding: "utf8",
        timeout: 300_000,
        env: { ...process.env, GOWORK: workspace.path, GOTOOLCHAIN: "go1.26.0" },
      });
    } finally {
      workspace.cleanup();
    }
    expect(JSON.parse(output)).toEqual({
      registeredOverRealCore: true,
      peerDirectoryPollAuthenticated: true,
      durableMarkerPersisted: true,
      staticInstanceNotMarked: true,
      restartRequiresReRegistration: true,
      reRegisteredAfterRestart: true,
      markerSingleAfterReregister: true,
    });
  }, 300_000);
});