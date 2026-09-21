import { readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

async function directories(path: string): Promise<string[]> {
  const entries = await readdir(path, { withFileTypes: true });
  return entries.filter((entry) => entry.isDirectory()).map((entry) => entry.name).sort();
}

async function goFiles(path: string): Promise<string[]> {
  const entries = await readdir(path, { withFileTypes: true });
  return entries.filter((entry) => entry.isFile() && entry.name.endsWith(".go")).map((entry) => join(path, entry.name));
}

describe("Gateway architecture", () => {
  it("keeps the domain limited to models and interfaces", async () => {
    const domain = join(root, "internal", "domain");
    expect(await directories(domain)).toEqual(["interfaces", "models"]);
    expect(await goFiles(domain)).toEqual([]);
  });

  it("uses one domain declaration per file", async () => {
    for (const group of ["models", "interfaces"]) {
      const files = await goFiles(join(root, "internal", "domain", group));
      for (const file of files) {
        const source = await readFile(file, "utf8");
        const declarations = source.match(/^type\s+[A-Za-z_][A-Za-z0-9_]*/gm) ?? [];
        expect(declarations, file).toHaveLength(1);
      }
    }
  });

  it("keeps application flat and infrastructure in approved groups", async () => {
    expect(await directories(join(root, "internal", "application"))).toEqual([]);
    expect(await directories(join(root, "internal", "infrastructure"))).toEqual([
      "config",
      "network",
      "observability",
      "plugins",
      "security",
      "storage",
    ]);
  });

  it("limits presentation to api and cli and keeps one command root", async () => {
    expect(await directories(join(root, "internal", "presentation"))).toEqual(["api", "cli"]);
    expect(await directories(join(root, "cmd"))).toEqual(["gateway"]);
  });
});
