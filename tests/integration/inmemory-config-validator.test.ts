import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("in-memory Management YAML validation", () => {
  it("does not require writable temporary storage", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-yaml-validation-"));
    const blockedTemporaryPath = join(directory, "not-a-directory");
    const binary = join(directory, "validator-probe");
    try {
      await writeFile(blockedTemporaryPath, "not a directory");
      await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/inmemory-config-validator"], { cwd: coreRoot });
      await expect(execFileAsync(binary, [], {
        cwd: coreRoot,
        env: { ...process.env, TMPDIR: blockedTemporaryPath },
      })).resolves.toBeDefined();
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
