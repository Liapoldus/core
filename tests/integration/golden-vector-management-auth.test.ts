import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

interface ManagementAuthVector {
  id: string;
  input: { bearer: boolean };
  expected: { authorized: boolean };
}

describe("Core management authentication golden vectors", () => {
  it("executes the Management API bearer authorization vector independently of TLS", async () => {
    const document = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/golden-vectors.json"), "utf8"),
    ) as { vectors: ManagementAuthVector[] };
    const vector = document.vectors.find(
      (candidate) => candidate.id === "management-api-requires-bearer",
    );
    expect(vector, "remote management authorization vector").toBeDefined();

    const directory = await mkdtemp(join(tmpdir(), "liapoldus-management-vector-"));
    try {
      const vectorPath = join(directory, "vector.json");
      await writeFile(vectorPath, JSON.stringify(vector), "utf8");
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/golden-vector-management-auth", vectorPath],
        { cwd: coreRoot },
      );
      const observed = JSON.parse(result.stdout) as { authorized: boolean; status: number };

      expect(observed.authorized).toBe(vector!.expected.authorized);
      expect(observed.status).toBeGreaterThanOrEqual(400);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
