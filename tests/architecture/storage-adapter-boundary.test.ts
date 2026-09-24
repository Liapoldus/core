import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("SQLite infrastructure boundaries", () => {
  it("loads static contracts outside the storage adapter", async () => {
    const source = await readFile(join(root, "internal", "infrastructure", "storage", "sqlite.go"), "utf8");
    expect(source).not.toContain('github.com/Liapoldus/core"');
    expect(source).not.toContain("gopkg.in/yaml.v3");
  });

  it("declares the SQLite driver as a storage-only vendor dependency", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");
    expect(architecture).toMatch(/sqliteDriver:\s*\{ in: \[modernc\.org\/sqlite\] \}/);
    expect(architecture).toMatch(/infrastructureStorage:[\s\S]*?canUse: \[[^\]]*sqliteDriver[^\]]*\]/);
  });
});
