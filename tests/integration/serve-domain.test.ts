import { execFile } from "node:child_process";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { createGoWorkspace, missingWorkspaceRepositories, workspaceRoot } from "../support/workspace.js";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const missing = missingWorkspaceRepositories("plugin-sdk", "pluginprotocol", "plugins/domain", "plugins/runtime");

describe.skipIf(missing.length > 0)("real Core -> SDK -> Domain data plane", () => {
  it("applies Core-owned settings and enforces Domain grants through Runtime over authenticated pluginprotocol", async () => {
    const workspace = createGoWorkspace("core", "plugin-sdk", "pluginprotocol", "plugins/domain", "plugins/runtime");
    try {
      const result = await execFileAsync("go", [
        "run", "./tests/fixtures/serve-domain", `${workspaceRoot()}/plugins/domain`, `${workspaceRoot()}/plugins/runtime`,
      ], {
        cwd: coreRoot,
        encoding: "utf8",
        timeout: 300_000,
        env: { ...process.env, GOWORK: workspace.path, GOTOOLCHAIN: "go1.26.0" },
      });
      expect(JSON.parse(result.stdout)).toEqual({
        generationApplied: true,
        invalidCandidateRefusedAndStateRetained: true,
        runtimeInvalidGenerationRefusedAndRecovered: true,
        coreConfiguredModel: true,
        grantedWriteAccepted: true,
        droppedWriteResponseObserved: true,
        retryAfterDroppedResponseRecovered: true,
        crossProcessWriteIDConflictRejected: true,
        globalReadAccepted: true,
        ungrantedWriteDenied: true,
        readOnlyQueryAccepted: true,
        grantGenerationUpdateApplied: true,
        existingDataRetained: true,
        newGrantApplied: true,
        removedGrantDenied: true,
        modelGenerationApplied: true,
        modelGenerationClearedData: true,
        newModelValidated: true,
        runtimeReadPathAccepted: true,
        queryResultLimitEnforced: true,
        transactionResultLimitIsAtomic: true,
        runtimeGrantDenied: true,
        runtimeCoreGrantApplied: true,
        runtimeWriteAccepted: true,
        runtimeWriteVisibleToOtherPlugin: true,
        runtimeEntityNarrowingDenied: true,
        runtimeMethodNarrowingDenied: true,
        domainRestartLostVolatileData: true,
        domainReconnectRestoredGeneration: true,
        runtimeHostCallReconnected: true,
        runtimeRestartReloadedCoreGeneration: true,
        runtimeGracefulDeregisterObserved: true,
        runtimeDrainedAdmittedCall: true,
        runtimeRejectedCallAfterDrain: true,
        runtimeCoreLinkRevocationDenied: true,
      });
    } finally {
      workspace.cleanup();
    }
  }, 300_000);
});
