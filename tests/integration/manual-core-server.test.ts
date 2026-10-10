import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { createGoWorkspace, missingWorkspaceRepositories } from "../support/workspace.js";

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
    const workspace = createGoWorkspace("core", "plugin-sdk", "pluginprotocol", "plugins/server", "plugins/forms-db");
    let output: string;
    try {
      output = execFileSync("go", ["run", "./tests/fixtures/manual-core-server"], {
        cwd: root,
        encoding: "utf8",
        timeout: 300_000,
        env: { ...process.env, GOWORK: workspace.path, GOTOOLCHAIN: "go1.26.0" },
      });
    } finally {
      workspace.cleanup();
    }
    expect(JSON.parse(output)).toEqual({
      first: "old", second: "new", afterRollback: "old",
      active: 1, previous: 2, exactDigest: true,
      formsActive: 2,
      restartedServer: "old",
      sitePublishAccepted: true,
      siteOperationCompleted: true,
      publishedSite: "published-site",
      restartedPublishedSite: "published-site",
      coreRestartConverged: true,
      serverRestartConverged: true,
      coreUnavailableFailsClosed: true,
      coreUnavailableRecovered: true,
      pluginControlRevocationEnforced: true,
      coordinatedTrustRootRotationConverged: true,
      serverReconnectConverged: true,
      formsSubmit: true,
      formsAdminQueryRoundTrip: true,
      formsAdminCursorGrantRoundTrip: true,
      formsAdminDeleteSemantics: true,
      staleProductionGrantDenied: true,
      formsListWithoutLeaseRejected: true,
      formsListRevokedReplicaDenied: true,
      incomingCookiesRedactedEverywhere: true,
      coreDidNotObservePeerPayload: true,
      formsDatabaseDriver: "memory",
      formsListRoundTrip: true,
      formsConcurrentRoundTrip: true,
      formsCandidateRefusalPreserved: false,
      formsCandidateRollbackRecovered: false,
      formsListAfterCandidateRollback: false,
      formsPeerReconnected: true,
      formsStorageUnavailableDuringOutage: false,
      formsStorageRecoveredAfterOutage: false,
      childNoSQLDSNEnvironment: true,
      formsDataPersistedAfterRestart: false,
    });
  }, 300_000);
});
