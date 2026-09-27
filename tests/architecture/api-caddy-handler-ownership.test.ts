import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");

describe("API Caddy handler ownership", () => {
  it("places Caddy Admin API forwarding and path validation in handlers/caddy.go", async () => {
    const source = await readFile(join(apiRoot, "handlers", "caddy.go"), "utf8").catch(() => "");
    const deps = await readFile(join(apiRoot, "handlers", "deps.go"), "utf8");

    expect(source).toMatch(/^func CaddyAdmin\(/m);
    expect(source).toMatch(/^func invalidCaddyPathSegment\(/m);
    expect(source).toMatch(/^func containsAnyString\(/m);
    expect(source).toMatch(/^func containsString\(/m);
    expect(deps).toMatch(/^type CaddyDependencies struct \{/m);
  });

  it("routes the Caddy management prefix through the handlers package", async () => {
    const source = await readFile(join(apiRoot, "router.go"), "utf8");

    expect(source).toMatch(/handlers\.CaddyAdmin\(/);
  });

  it("removes the Caddy Admin handler from the legacy adapter", async () => {
    const source = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");

    expect(source).not.toMatch(/^func \(server \*Server\) handleCaddyAdmin\(/m);
    expect(source).not.toMatch(/^func invalidCaddyPathSegment\(/m);
    expect(source).not.toMatch(/^func containsAnyString\(/m);
    expect(source).not.toMatch(/^func containsString\(/m);
  });
});
