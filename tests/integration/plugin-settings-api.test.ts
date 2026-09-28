import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("plugin settings read API", () => {
  it("returns the active SQLite revision, digest and config with a strong ETag", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-plugin-settings-api-"));
    try {
      const result = await execFileAsync("go", ["run", "./tests/fixtures/plugin-settings-api", join(directory, "gateway.db")], { cwd: coreRoot });
      expect(JSON.parse(result.stdout)).toEqual({
        status: 200,
        etag: '"1"',
        settings: { revision: "1", digest: expect.stringMatching(/^[a-f0-9]{64}$/), config: { mode: "fixture" } },
        missingStatus: 404,
        missingCode: "plugin_not_found",
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 30_000);
});
