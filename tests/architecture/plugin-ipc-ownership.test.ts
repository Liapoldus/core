import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("plugin IPC contract ownership", () => {
  it("keeps plugin wire schemas in pluginprotocol instead of a Gateway mirror", async () => {
    const legacyContract = join(coreRoot, "contracts/v1/plugin-contracts.json");
    const manifest = JSON.parse(await readFile(join(coreRoot, "contracts/v1/manifest.json"), "utf8")) as {
      files: Record<string, string>;
    };

    expect(existsSync(legacyContract)).toBe(false);
    expect(manifest.files).not.toHaveProperty("plugin-contracts.json");
  });

  it("does not retain the unconnected Gateway-specific WAF wire adapter", async () => {
    const adapter = await readFile(join(coreRoot, "internal/infrastructure/plugins/capability_dispatch.go"), "utf8");
    const fixture = await readFile(join(coreRoot, "tests/fixtures/plugin-grpc/main.go"), "utf8");

    expect(adapter).not.toMatch(/WAFContext|WAFDecision|\.WAF\(/);
    expect(adapter).not.toContain('json:"waf,omitempty"');
    expect(fixture).not.toMatch(/WAF\s+\*struct|input\.WAF/);
  });
});
