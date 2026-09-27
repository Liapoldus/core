import { execFile } from "node:child_process";
import { mkdtemp, rm, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

interface DriftObservation {
  readonly mutationStatus: number;
  readonly publishStatus: number;
  readonly publishCode: string;
  readonly drift: boolean;
  readonly currentRevisionUnchanged: boolean;
  readonly revisionCountUnchanged: boolean;
}

describe("Caddy runtime drift and group publish", () => {
  it("rejects publication before staging when runtime differs from its checkpoint", async () => {
    const vectorDocument = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/golden-vectors.json"), "utf8"),
    ) as { vectors: Array<{ id: string; expected: { drift?: boolean; publishStatus?: number; code?: string } }> };
    const vector = vectorDocument.vectors.find((candidate) => candidate.id === "admin-drift-blocks-group-publish");
    expect(vector).toBeDefined();

    const directory = await mkdtemp(join(tmpdir(), "liapoldus-admin-drift-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/admin-mutation-checkpoint", join(directory, "gateway.db"), "drift"],
        { cwd: coreRoot },
      );
      const observed = JSON.parse(result.stdout) as DriftObservation;

      expect(observed.mutationStatus).toBe(503);
      expect(observed.drift).toBe(vector!.expected.drift);
      expect(observed.publishStatus, JSON.stringify(observed)).toBe(vector!.expected.publishStatus);
      expect(observed.publishCode).toBe(vector!.expected.code);
      expect(observed.currentRevisionUnchanged).toBe(true);
      expect(observed.revisionCountUnchanged).toBe(true);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 30_000);
});
