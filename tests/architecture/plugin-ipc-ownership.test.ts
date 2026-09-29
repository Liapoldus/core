import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("plugin IPC contract ownership", () => {
  it("keeps plugin wire schemas in pluginprotocol instead of a Core mirror", async () => {
    const legacyContract = join(coreRoot, "contracts/v1/plugin-contracts.json");
    const manifest = JSON.parse(await readFile(join(coreRoot, "contracts/v1/manifest.json"), "utf8")) as {
      files: Record<string, string>;
    };

    expect(existsSync(legacyContract)).toBe(false);
    expect(manifest.files).not.toHaveProperty("plugin-contracts.json");
  });

});
