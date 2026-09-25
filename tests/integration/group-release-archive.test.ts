import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("group release archive boundary", () => {
  it("rejects traversal entries with the documented artifact problem before activation", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-group-archive-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/group-management-api", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const report = JSON.parse(result.stdout) as {
        unsafeArtifactStatus: number;
        unsafeArtifactCode: string;
      };

      expect(report.unsafeArtifactStatus).toBe(422);
      expect(report.unsafeArtifactCode).toBe("artifact_invalid");
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 15_000);
});
