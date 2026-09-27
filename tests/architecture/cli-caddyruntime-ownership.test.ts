import { readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { readGoPackageSources } from "../support/presentation-source.js";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const cliRoot = join(root, "internal", "presentation", "cli");
const caddyRuntime = join(cliRoot, "caddyruntime");

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

describe("CLI Caddy runtime ownership", () => {
  it("keeps runtime type definitions out of the CLI root", async () => {
    const source = await directGoSources(cliRoot);

    expect(/^type\s+RuntimeBindings\s+struct\s*\{/m.test(source)).toBe(false);
    expect(/^type\s+PluginDispatchBinding\s+struct\s*\{/m.test(source)).toBe(false);
    expect(/^type\s+CaddyRuntime\s+interface\s*\{/m.test(source)).toBe(false);
    expect(/^type\s+lazyCaddyActivator\s+struct\s*\{/m.test(source)).toBe(false);
    expect(/^func\s+newSystemCaddyActivator\s*\(/m.test(source)).toBe(false);
    const aliases = [...source.matchAll(/^type\s+(RuntimeBindings|PluginDispatchBinding|CaddyRuntime)\s*=\s*caddyruntime\.(?:RuntimeBindings|PluginDispatchBinding|CaddyRuntime)\s*$/gm)]
      .map((alias) => alias[1])
      .sort();
    expect(aliases).toEqual(["CaddyRuntime", "PluginDispatchBinding", "RuntimeBindings"]);
  });

  it("places the runtime implementation in the four agreed leaf files", async () => {
    let files: string[] = [];
    try {
      files = (await readdir(caddyRuntime, { withFileTypes: true }))
        .filter((entry) => entry.isFile() && entry.name.endsWith(".go"))
        .map((entry) => entry.name)
        .sort();
    } catch {
      files = [];
    }

    expect(files).toEqual(["activator.go", "bindings.go", "cookie_policy.go", "system.go"]);
  });

  it("owns Caddy runtime bindings, activation, cookie policy, and system composition", async () => {
    const bindings = await directGoSources(caddyRuntime);
    const packageSources = await readGoPackageSources(root, "internal/presentation/cli/caddyruntime");

    expect(bindings).toMatch(/^type\s+RuntimeBindings\s+struct\s*\{/m);
    expect(bindings).toMatch(/^type\s+PluginDispatchBinding\s+struct\s*\{/m);
    expect(bindings).toMatch(/^type\s+CaddyRuntime\s+interface\s*\{/m);
    expect(packageSources).toMatch(/^type\s+lazyCaddyActivator\s+struct\s*\{/m);
    expect(packageSources).toMatch(/^func\s+\(activator \*lazyCaddyActivator\) ActivatePluginCookiePolicy\s*\(/m);
    expect(packageSources).toMatch(/^func\s+NewSystemCaddyActivator\s*\(/m);
    expect(packageSources).not.toContain("github.com/Liapoldus/core/internal/presentation/cli\"");
  });
});
