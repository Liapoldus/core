import { readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const cliRoot = join(root, "internal", "presentation", "cli");

async function directGoSources(directory: string, excludedFile = ""): Promise<string> {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = entries
    .filter((entry) => entry.isFile() && entry.name.endsWith(".go") && entry.name !== excludedFile)
    .map((entry) => join(directory, entry.name));
  return (await Promise.all(files.map((file) => readFile(file, "utf8")))).join("\n");
}

describe("CLI entrypoint ownership", () => {
  it("keeps access command implementation in access.go", async () => {
    const [rootSources, accessSource] = await Promise.all([
      directGoSources(cliRoot, "access.go"),
      readFile(join(cliRoot, "access.go"), "utf8").catch(() => ""),
    ]);

    expect(rootSources).not.toMatch(/^func\s+access\s*\(/m);
    expect(accessSource).toMatch(/^func\s+access\s*\(/m);
  });

  it("keeps serve command implementation in serve.go", async () => {
    const [rootSources, serveSource] = await Promise.all([
      directGoSources(cliRoot, "serve.go"),
      readFile(join(cliRoot, "serve.go"), "utf8").catch(() => ""),
    ]);

    expect(rootSources).not.toMatch(/^func\s+serve\s*\(/m);
    expect(serveSource).toMatch(/^func\s+serve\s*\(/m);
  });
});
