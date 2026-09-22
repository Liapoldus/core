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
});
