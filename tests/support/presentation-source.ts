import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

async function goFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(entries.map(async (entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return goFiles(path);
    return entry.isFile() && entry.name.endsWith(".go") ? [path] : [];
  }));
  return nested.flat().sort();
}

export async function readGoPackageSources(root: string, packagePath: string): Promise<string> {
  const files = await goFiles(join(root, packagePath));
  return (await Promise.all(files.map((file) => readFile(file, "utf8")))).join("\n");
}
