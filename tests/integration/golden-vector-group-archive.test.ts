import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

interface ArchiveTraversalVector {
  id: string;
  input: {
    entry?: string;
    entries?: Array<{ name: string; content: string }>;
    corruptGzip?: boolean;
    entryDepth?: number;
    entryPathBytes?: number;
    entryCount?: number;
    contentBytes?: number;
  };
  expected: { accepted: boolean; code: string; activeRevisionChanged: boolean };
}

describe("Gateway archive golden-vector conformance", () => {
  it.each([
    "archive-traversal",
    "archive-gzip-integrity",
    "archive-duplicate-path",
    "archive-case-collision",
    "archive-nfc-normalization",
    "archive-path-depth-limit",
    "archive-path-byte-limit",
    "archive-entry-count-limit",
    "archive-compression-ratio-limit",
  ])("executes %s against the group-release Management API", async (vectorID) => {
    const document = JSON.parse(
      await readFile(join(coreRoot, "contracts/v1/golden-vectors.json"), "utf8"),
    ) as { vectors: ArchiveTraversalVector[] };
    const vector = document.vectors.find((candidate) => candidate.id === vectorID);
    expect(vector, `${vectorID} vector`).toBeDefined();

    const directory = await mkdtemp(join(tmpdir(), "liapoldus-golden-archive-vector-"));
    try {
      const vectorPath = join(directory, "vector.json");
      const databasePath = join(directory, "gateway.db");
      await writeFile(vectorPath, JSON.stringify(vector), "utf8");
      const result = await execFileAsync(
        "go",
        ["run", "./tests/fixtures/golden-vector-group-archive", vectorPath, databasePath],
        { cwd: coreRoot },
      );
      const observed = JSON.parse(result.stdout) as {
        accepted: boolean;
        code: string;
        activeRevisionChanged: boolean;
      };

      expect(observed.accepted).toBe(vector!.expected.accepted);
      expect(observed.code).toBe(vector!.expected.code);
      expect(observed.activeRevisionChanged).toBe(vector!.expected.activeRevisionChanged);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 30_000);
});
