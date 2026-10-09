import { readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { readGoPackageSources } from "../support/presentation-source.js";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const runtimeRoot = join(root, "internal", "runtime");
const runtimePath = "internal/runtime";

async function directGoSources(directory: string): Promise<string> {
  let entries;
  try {
    entries = await readdir(directory, { withFileTypes: true });
  } catch {
    return "";
  }
  const files = entries
    .filter((entry) => entry.isFile() && entry.name.endsWith(".go"))
    .map((entry) => join(directory, entry.name));
  return (await Promise.all(files.map((file) => readFile(file, "utf8")))).join("\n");
}

describe("Core runtime ownership", () => {
  it("places management TLS setup in the bootstrap leaf package", async () => {
    const runtimeSources = await directGoSources(runtimeRoot);

    expect(runtimeSources).toMatch(/^func\s+ManagementTLS\s*\(/m);
    expect(runtimeSources).not.toContain("github.com/Liapoldus/core/internal/presentation/cli\"");
  });

  it("places SQLite bootstrap opening in the stores leaf", async () => {
    const runtimeSources = await directGoSources(runtimeRoot);

    expect(runtimeSources).toMatch(/^func\s+OpenDatabase\s*\(/m);
    expect(runtimeSources).not.toContain("github.com/Liapoldus/core/internal/presentation/cli\"");
  });

  it("places plugin inventory presentation in the inventory leaf", async () => {
    const runtimeSources = await directGoSources(runtimeRoot);

    expect(runtimeSources).toMatch(/^func\s+PresentPluginInventory\s*\(/m);
    expect(runtimeSources).not.toContain("github.com/Liapoldus/core/internal/presentation/cli\"");
  });

  it("moves bootstrap phase orchestration out of the CLI package", async () => {
    const runtimeSources = await directGoSources(runtimeRoot);
    const runtimeEntries = await readdir(runtimeRoot, { withFileTypes: true });

    expect(runtimeSources).toMatch(/^func\s+Serve\s*\(/m);
    expect(runtimeSources).toMatch(/^type\s+RunOptions\s+struct\s*\{/m);
    expect(runtimeEntries.some((entry) => entry.name === "serve_bootstrap.go")).toBe(false);
  });
});
