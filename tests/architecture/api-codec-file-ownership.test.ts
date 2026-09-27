import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");

describe("API codec ownership", () => {
  it("keeps plugin-cookie payload DTOs and slice projection in codec.go", async () => {
    const source = await readFile(join(apiRoot, "codec.go"), "utf8").catch(() => "");

    expect(source).toMatch(/^type pluginCookiePolicyInput struct \{/m);
    expect(source).toMatch(/^func sliceValues\(/m);
  });

  it("removes codec declarations from the legacy adapter file", async () => {
    const source = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");

    expect(source).not.toMatch(/^type pluginCookiePolicyInput struct \{/m);
    expect(source).not.toMatch(/^func sliceValues\(/m);
  });
});
