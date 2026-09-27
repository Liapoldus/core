import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");

describe("API codec ownership", () => {
  it("keeps payload DTOs, release decoding, and response projections in codec.go", async () => {
    const source = await readFile(join(apiRoot, "codec.go"), "utf8").catch(() => "");

    expect(source).toMatch(/^type groupReleaseMetadata struct \{/m);
    expect(source).toMatch(/^type pluginCookiePolicyInput struct \{/m);
    expect(source).toMatch(/^func readGroupReleaseMultipart\(/m);
    expect(source).toMatch(/^func \(server \*Server\) groupResponse\(/m);
    expect(source).toMatch(/^func sliceValues\(/m);
    expect(source).toMatch(/^func ascii\(/m);
  });

  it("removes codec declarations from the legacy adapter file", async () => {
    const source = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");

    expect(source).not.toMatch(/^type groupReleaseMetadata struct \{/m);
    expect(source).not.toMatch(/^type pluginCookiePolicyInput struct \{/m);
    expect(source).not.toMatch(/^func readGroupReleaseMultipart\(/m);
    expect(source).not.toMatch(/^func \(server \*Server\) groupResponse\(/m);
    expect(source).not.toMatch(/^func sliceValues\(/m);
    expect(source).not.toMatch(/^func ascii\(/m);
  });
});
