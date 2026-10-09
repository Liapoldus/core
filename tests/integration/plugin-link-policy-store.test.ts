import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

describe("SQLite peer link policy store", () => {
  it("round-trips rules, enforces CAS and commits audit with each mutation", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-link-policy-"));
    try {
      const { stdout } = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/plugin-link-policy-store", join(directory, "core.db")],
        { cwd: coreRoot },
      );
      expect(JSON.parse(stdout)).toEqual({
        createdRevision: 1,
        loadedPlacements: ["same-placement", "remote"],
        loadedCarriers: ["unix", "tcp"],
        loadedWeight: 5,
        loadedContractIDs: ["example.contract"],
        duplicateConflict: true,
        replacedRevision: 2,
        staleReplaceConflict: true,
        missingReplaceNotFound: true,
        staleDeleteConflict: true,
        deleteSucceeded: true,
        deleteMissingNotFound: true,
        remainingPolicies: 1,
        auditEvents: 4,
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});