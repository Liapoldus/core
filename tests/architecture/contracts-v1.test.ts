import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));

describe("versioned Gateway contracts", () => {
  it("matches the manifest checksums", async () => {
    const directory = join(root, "contracts", "v1");
    const manifest = JSON.parse(await readFile(join(directory, "manifest.json"), "utf8")) as { version: string; files: Record<string, string> };
    expect(manifest.version).toBe("liapoldus.gateway.v1");
    for (const [name, expected] of Object.entries(manifest.files)) {
      const digest = createHash("sha256").update(await readFile(join(directory, name))).digest("hex");
      expect(`sha256:${digest}`, name).toBe(expected);
    }
  });

  it("contains the complete v1 golden-vector catalog", async () => {
    const document = JSON.parse(await readFile(join(root, "contracts", "v1", "golden-vectors.json"), "utf8")) as { version: string; vectors: Array<{ id: string; input: unknown; expected: unknown }> };
    expect(document.version).toBe("1.0.0");
    expect(document.vectors).toHaveLength(50);
    expect(new Set(document.vectors.map((vector) => vector.id)).size).toBe(50);
    for (const vector of document.vectors) {
      expect(vector.input, vector.id).toBeDefined();
      expect(vector.expected, vector.id).toBeDefined();
    }
  });
});
