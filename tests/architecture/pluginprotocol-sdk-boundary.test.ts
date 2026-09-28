import { readFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = fileURLToPath(new URL("../..", import.meta.url));

async function goFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(entries.map(async (entry) => {
    const path = `${directory}/${entry.name}`;
    return entry.isDirectory() ? goFiles(path) : entry.name.endsWith(".go") ? [path] : [];
  }));
  return files.flat();
}

describe("Core plugin protocol boundary", () => {
  it("uses only the protocol SDK facade, never the low-level transport package", async () => {
    const files = await goFiles(`${root}/internal`);
    const directImports = await Promise.all(files.map(async (file) => ({
      file,
      source: await readFile(file, "utf8"),
    })));
    const offenders = directImports.filter(({ source }) =>
      /github\.com\/Liapoldus\/pluginprotocol\/transport/.test(source),
    );
    expect(offenders.map(({ file }) => file.replace(`${root}/`, ""))).toEqual([]);
  });

  it("delegates plugin and grant listener creation to pluginprotocol", async () => {
    const runtime = await readFile(`${root}/internal/infrastructure/plugins/runtime.go`, "utf8");
    expect(runtime).not.toMatch(/net\.Listen(?:TCP)?\s*\(/);
    expect(runtime).toMatch(/pluginsdk\.ListenLoopback\(\)/);
    expect(runtime).toMatch(/pluginsdk\.StartGrantBroker\(/);
  });
});
