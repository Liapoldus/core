import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("plugin Manifest capability-to-mode lookup", () => {
  it("accepts every declared protocol invocation mode and rejects undeclared pairs", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-manifest-mode-lookup-"));
    const binary = join(directory, "manifest-check");
    try {
      await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-manifest-check"], { cwd: coreRoot });
      const modes = [
        "INVOCATION_MODE_CALL",
        "INVOCATION_MODE_HTTP_STREAM",
        "INVOCATION_MODE_WEBSOCKET",
        "INVOCATION_MODE_SSE",
        "INVOCATION_MODE_TCP",
        "INVOCATION_MODE_UDP",
      ];

      for (const mode of modes) {
        const result = await execFileAsync(binary, [JSON.stringify({
          manifest: {
            name: "fixture",
            protocolVersion: "liapoldus.plugin.v1",
            capabilities: ["forms.submit"],
            capabilityDescriptors: [{ capability: "forms.submit", modes: [mode] }],
          },
          required: ["forms.submit"],
          capability: "forms.submit",
          mode,
        })], { cwd: coreRoot });
        expect(JSON.parse(result.stdout)).toEqual({ valid: true, modeSupported: true });
      }

      const undeclaredPairs = [
        { capability: "forms.missing", mode: "INVOCATION_MODE_CALL" },
        { capability: "forms.submit", mode: "INVOCATION_MODE_WEBSOCKET" },
      ];
      for (const pair of undeclaredPairs) {
        const result = await execFileAsync(binary, [JSON.stringify({
          manifest: {
            name: "fixture",
            protocolVersion: "liapoldus.plugin.v1",
            capabilities: ["forms.submit"],
            capabilityDescriptors: [{ capability: "forms.submit", modes: ["INVOCATION_MODE_CALL"] }],
          },
          required: [],
          ...pair,
        })], { cwd: coreRoot });
        expect(JSON.parse(result.stdout)).toEqual({ valid: true, modeSupported: false });
      }
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
