import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");

describe("plugin-owned administration boundary", () => {
  it("exposes management only through generic plugin surfaces", async () => {
    const [router, openapi] = await Promise.all([
      readFile(join(apiRoot, "router.go"), "utf8"),
      readFile(join(root, "contracts", "v1", "management.openapi.yaml"), "utf8"),
    ]);

    expect(router).toContain("AdminPages");
    expect(router).toContain("Plugins");
    expect(openapi).toContain("/api/plugins");
    expect(openapi).not.toContain("/api/sites");
  });
});
