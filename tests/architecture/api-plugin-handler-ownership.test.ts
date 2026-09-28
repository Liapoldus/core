import { readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const apiRoot = join(root, "internal", "presentation", "api");

async function readGoTree(directory: string): Promise<string> {
  const entries = await readdir(directory, { withFileTypes: true });
  const sources = await Promise.all(entries.map(async (entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return readGoTree(path);
    if (!entry.isFile() || !entry.name.endsWith(".go")) return "";
    return readFile(path, "utf8");
  }));
  return sources.join("\n");
}

describe("API plugin handler ownership", () => {
  it("places plugin lifecycle, detail, and admin handlers in handlers", async () => {
    const handlers = await readGoTree(join(apiRoot, "handlers"));
    const dependencies = await readFile(join(apiRoot, "handlers", "deps.go"), "utf8");

    for (const handler of [
      "PluginList",
      "AdminSurfaceList",
      "PluginRestart",
      "IsPluginDetailPath",
      "PluginDetail",
      "PluginAdmin",
    ]) {
      expect(handlers).toMatch(new RegExp(`^func ${handler}\\(`, "m"));
    }
    expect(dependencies).toMatch(/^type PluginDependencies struct \{/m);
  });

  it("routes all plugin endpoints through the typed handlers boundary", async () => {
    const router = await readFile(join(apiRoot, "router.go"), "utf8");

    for (const handler of [
      "PluginList",
      "AdminSurfaceList",
      "PluginRestart",
      "PluginDetail",
      "PluginAdmin",
    ]) {
      expect(router).toMatch(new RegExp(`handlers\\.${handler}\\(`));
    }
  });

  it("removes plugin request handling and plugin DTOs from the API root", async () => {
    const adapter = await readFile(join(apiRoot, "adapter.go"), "utf8").catch(() => "");
    const codec = await readFile(join(apiRoot, "codec.go"), "utf8");

    for (const method of [
      "handlePluginRestart",
      "isPluginDetailPath",
      "handlePluginDetail",
      "handlePluginAdmin",
    ]) {
      expect(adapter).not.toMatch(new RegExp(`^func \\(server \\*Server\\) ${method}\\(`, "m"));
    }
  });
});
