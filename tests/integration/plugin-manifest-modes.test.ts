import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("plugin Manifest capability invocation modes", () => {
  it("requires exact descriptors and at least one supported mode for every capability", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-manifest-modes-"));
    const binary = join(directory, "manifest-check");
    try {
      await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-manifest-check"], { cwd: coreRoot });
      const valid = {
        name: "identity",
        capabilities: ["identity.login", "identity.validate"],
        protocolVersion: "liapoldus.plugin.v1",
        capabilityDescriptors: [
          { capability: "identity.login", modes: ["INVOCATION_MODE_CALL"] },
          { capability: "identity.validate", modes: ["INVOCATION_MODE_CALL"] },
        ],
      };
      const cases = [
        { manifest: valid, required: ["identity.login"], expected: true },
        { manifest: { ...valid, capabilityDescriptors: valid.capabilityDescriptors.slice(0, 1) }, required: [], expected: false },
        { manifest: { ...valid, capabilities: ["identity.login"], capabilityDescriptors: [...valid.capabilityDescriptors, { capability: "identity.unknown", modes: ["INVOCATION_MODE_CALL"] }] }, required: [], expected: false },
        { manifest: { ...valid, capabilityDescriptors: [{ capability: "identity.login", modes: ["INVOCATION_MODE_UNSPECIFIED"] }, valid.capabilityDescriptors[1]] }, required: [], expected: false },
        { manifest: { ...valid, capabilityDescriptors: [{ capability: "identity.login", modes: ["INVOCATION_MODE_CALL", "INVOCATION_MODE_CALL"] }, valid.capabilityDescriptors[1]] }, required: [], expected: false },
        { manifest: valid, required: ["identity.missing"], expected: false },
      ];

      for (const testCase of cases) {
        const result = await execFileAsync(binary, [JSON.stringify({ manifest: testCase.manifest, required: testCase.required })], { cwd: coreRoot });
        expect(JSON.parse(result.stdout)).toEqual({ valid: testCase.expected });
      }
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
