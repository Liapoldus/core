import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
describe("plugin cookie contract ownership", () => {
  it("uses pluginprotocol types without keeping a Gateway copy of the IPC schema", async () => {
    const adapter = await readFile(join(coreRoot, "internal/infrastructure/plugins/capability_dispatch.go"), "utf8");

    expect(existsSync(join(coreRoot, "contracts/v1/plugin-contracts.json"))).toBe(false);
    expect(adapter).toContain('plugincontracts "github.com/Liapoldus/pluginprotocol"');
    expect(adapter).toContain("type CookiePair = plugincontracts.CookiePair");
    expect(adapter).toContain("type CookiePolicy = plugincontracts.CookiePolicy");
    expect(adapter).toContain("[]plugincontracts.CookieAction");
    expect(adapter).toContain("plugincontracts.DecodeHTTPResponseAction");
  });
});
