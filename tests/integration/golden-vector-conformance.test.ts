import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

interface Vector {
  id: string;
  input: { keys?: string[] };
  expected: { valid?: boolean; code?: string };
}

describe("Gateway golden-vector conformance", () => {
  it("executes the bootstrap unknown-field vector through the production validator", async () => {
    const vectorDocument = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/golden-vectors.json"), "utf8"),
    ) as { vectors: Vector[] };
    const schema = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/gateway.schema.json"), "utf8"),
    ) as { properties: Record<string, unknown> };
    const errors = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/errors.json"), "utf8"),
    ) as { errors: Array<{ code: string; status: number }> };
    const vector = vectorDocument.vectors.find(
      (candidate) => candidate.input.keys !== undefined && candidate.expected.valid === false,
    );
    expect(vector, "bootstrap rejection vector").toBeDefined();
    const unknownKeys = vector!.input.keys!.filter((key) => !(key in schema.properties));
    expect(unknownKeys).toHaveLength(1);

    const directory = await mkdtemp(join(tmpdir(), "liapoldus-golden-vector-"));
    try {
      const template = await readFile(
        join(coreRoot, "tests/fixtures/golden-vectors/bootstrap-base.yaml"),
        "utf8",
      );
      const documentPath = join(directory, "gateway.yaml");
      await writeFile(documentPath, `${template}${unknownKeys[0]}: {}\n`, "utf8");
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/golden-vector-bootstrap", documentPath],
        { cwd: coreRoot },
      );
      const observed = JSON.parse(result.stdout) as { valid: boolean; code: string; status: number };

      expect(observed.valid).toBe(vector!.expected.valid);
      expect(observed.code).toBe(vector!.expected.code);
      expect(observed.status).toBe(
        errors.errors.find((problem) => problem.code === vector!.expected.code)?.status,
      );
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
