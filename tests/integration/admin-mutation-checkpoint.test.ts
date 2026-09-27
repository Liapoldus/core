import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = dirname(dirname(dirname(fileURLToPath(import.meta.url))));

interface CheckpointVector {
  id: string;
  input: { operation: string; checkpoint: string; forward: string };
  expected: { checkpointRetained: boolean; auditBodyStored: boolean };
}

interface MutationObservation {
  status: number;
  adminRequests: number;
  checkpointCountAtForward: number;
  auditCountAtForward: number;
  checkpointCountAfter: number;
  auditBodyStored: boolean;
}

describe("Caddy Admin mutation checkpoint ordering", () => {
  it("commits checkpoint metadata before forwarding a mutation and never audits its body", async () => {
    const vectors = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/golden-vectors.json"), "utf8"),
    ) as { vectors: CheckpointVector[] };
    const vector = vectors.vectors.find((candidate) => candidate.id === "admin-mutation-checkpoint-first");
    expect(vector).toBeDefined();

    const directory = await mkdtemp(join(tmpdir(), "liapoldus-admin-checkpoint-"));
    try {
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/admin-mutation-checkpoint", join(directory, "gateway.db")],
        { cwd: coreRoot },
      );
      const observed = JSON.parse(result.stdout) as MutationObservation;

      expect(observed.status).toBe(503);
      expect(observed.adminRequests).toBeGreaterThan(0);
      expect(observed.checkpointCountAtForward).toBeGreaterThan(0);
      expect(observed.auditCountAtForward).toBeGreaterThan(0);
      expect(observed.checkpointCountAfter > 0).toBe(vector!.expected.checkpointRetained);
      expect(observed.auditBodyStored).toBe(vector!.expected.auditBodyStored);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
