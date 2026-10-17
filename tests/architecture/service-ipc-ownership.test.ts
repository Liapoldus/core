import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

describe("service IPC contract ownership", () => {
  it("keeps service wire schemas in wool instead of a Core mirror", async () => {
    const legacyContract = join(coreRoot, "contracts/v1/service-contracts.json");
    const manifest = JSON.parse(await readFile(join(coreRoot, "contracts/v1/manifest.json"), "utf8")) as {
      files: Record<string, string>;
    };

    expect(existsSync(legacyContract)).toBe(false);
    expect(manifest.files).not.toHaveProperty("service-contracts.json");
  });

});
