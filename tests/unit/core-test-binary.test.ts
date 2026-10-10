import { existsSync } from "node:fs";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { fileURLToPath } from "node:url";

const execFileAsync = promisify(execFile);
const root = fileURLToPath(new URL("../..", import.meta.url));

describe("Core test binary lifecycle", () => {
  it("removes its temporary executable and directory on cleanup", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-core-binary-test-"));
    const executable = join(directory, "core");
    try {
      await execFileAsync("go", ["build", "-o", executable, "./cmd/core"], { cwd: root });
      expect(existsSync(executable)).toBe(true);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
    expect(existsSync(executable)).toBe(false);
  }, 60_000);
});
