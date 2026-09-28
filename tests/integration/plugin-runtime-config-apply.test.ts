import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("supervised plugin ConfigApply", () => {
  it("uses the protocol RPC and requires the plugin to acknowledge the exact revision", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-runtime-config-apply-"));
    const pluginBinary = join(directory, "plugin-child");
    try {
      await execFileAsync("go", ["build", "-o", pluginBinary, "./tests/fixtures/serve-plugin-child"], { cwd: coreRoot });
      const result = await execFileAsync("go", ["run", "./tests/fixtures/plugin-runtime-config-apply", pluginBinary], { cwd: coreRoot });
      expect(JSON.parse(result.stdout)).toEqual({
        rejectedApplyStayedInactive: true,
        mismatchedAckRejected: true,
        exactRevisionAcknowledged: true,
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  }, 60_000);
});
