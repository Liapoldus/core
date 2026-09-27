import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { dirname, join, basename } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))));

type PublicationFile = { source: string; target: string };

describe("published Gateway contract mirrors", () => {
  it("matches every data-declared asset and manifest digest", async () => {
    const publicationPath = join(root, "assets/contracts/publication.json");
    const publication = JSON.parse(await readFile(publicationPath, "utf8")) as {
      files: PublicationFile[];
    };
    const outputDirectory = join(root, "contracts/v1");
    const manifest = JSON.parse(await readFile(join(outputDirectory, "manifest.json"), "utf8")) as {
      files: Record<string, string>;
    };
    const sourceNames = publication.files.map((file) => file.source);
    const targetNames = publication.files.map((file) => file.target);

    expect(publication.files.length).toBeGreaterThan(0);
    expect(new Set(sourceNames).size).toBe(sourceNames.length);
    expect(new Set(targetNames).size).toBe(targetNames.length);

    for (const file of publication.files) {
      expect(basename(file.source), file.source).toBe(file.source);
      expect(basename(file.target), file.target).toBe(file.target);
      const source = await readFile(join(root, "assets/contracts", file.source));
      const published = await readFile(join(outputDirectory, file.target));
      const digest = createHash("sha256").update(source).digest("hex");

      expect(published.equals(source), file.target).toBe(true);
      expect(manifest.files[file.target], file.target).toBe(`sha256:${digest}`);
    }
  });
});
