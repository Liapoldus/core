import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");

describe("API server file ownership", () => {
  it("keeps Server construction and lifecycle in api/server.go", async () => {
    const source = await readFile(join(apiRoot, "server.go"), "utf8").catch(() => "");

    expect(source).toMatch(/^type Server struct \{/m);
    expect(source).toMatch(/^func \(server \*Server\) Handler\(/m);
    expect(source).toMatch(/^func \(server \*Server\) Listen\(/m);
  });

  it("does not leave Server type or lifecycle declarations in the legacy adapter file", async () => {
    const source = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");

    expect(source).not.toMatch(/^type Server struct \{/m);
    expect(source).not.toMatch(/^func \(server \*Server\) Handler\(/m);
    expect(source).not.toMatch(/^func \(server \*Server\) Listen\(/m);
  });
});
