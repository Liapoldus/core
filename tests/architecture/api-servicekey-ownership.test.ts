import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");

describe("API service-key handler ownership", () => {
  it("places service-key request handlers in the handlers package", async () => {
    const source = await readFile(join(apiRoot, "handlers", "servicekeys.go"), "utf8").catch(() => "");

    expect(source).toMatch(/^func ServiceKeyList\(/m);
    expect(source).toMatch(/^func ServiceKeyCreate\(/m);
  });

  it("routes service-key requests through the handlers package", async () => {
    const source = await readFile(join(apiRoot, "router.go"), "utf8");

    expect(source).toMatch(/handlers\.ServiceKeyList\(/);
    expect(source).toMatch(/handlers\.ServiceKeyCreate\(/);
  });

  it("removes service-key request handling from the legacy adapter", async () => {
    const source = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");

    expect(source).not.toMatch(/^func \(server \*Server\) handleServiceKeyList\(/m);
    expect(source).not.toMatch(/^func \(server \*Server\) handleServiceKeyCreate\(/m);
  });
});
