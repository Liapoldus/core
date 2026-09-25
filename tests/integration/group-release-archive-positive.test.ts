import { createHash } from "node:crypto";
import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("safe group release archive", () => {
  it("stages an allowed frontend root and returns its verified manifest", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-archive-positive-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-management-api", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        safeArtifactStatus: number;
        safeArtifactDetailStatus: number;
        safeArtifactDigest: string;
        safeFrontendId: string;
        safeFrontendDigest: string;
        safeFrontendFiles: number;
      };
      const fileDigest = createHash("sha256").update("content").digest();
      const frontendDigest = createHash("sha256")
        .update("index.html")
        .update(Buffer.from([0]))
        .update(fileDigest)
        .digest("hex");

      expect(report.safeArtifactStatus).toBe(202);
      expect(report.safeArtifactDetailStatus).toBe(200);
      expect(report.safeArtifactDigest).toMatch(/^[a-f0-9]{64}$/);
      expect(report.safeFrontendId).toBe("ui");
      expect(report.safeFrontendDigest).toBe(frontendDigest);
      expect(report.safeFrontendFiles).toBe(1);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 15_000);
});
