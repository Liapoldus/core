import { readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { readGoPackageSources } from "../support/presentation-source.js";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const cliRoot = join(root, "internal", "presentation", "cli");
const bootstrapPath = "internal/presentation/cli/bootstrap";

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

describe("CLI bootstrap ownership", () => {
  it("places management TLS setup in the bootstrap leaf package", async () => {
    const cliSources = await directGoSources(cliRoot);
    const bootstrapSources = await readGoPackageSources(root, bootstrapPath).catch(() => "");

    expect(cliSources).not.toMatch(/^func\s+managementTLS\s*\(/m);
    expect(bootstrapSources).toMatch(/^func\s+ManagementTLS\s*\(/m);
    expect(bootstrapSources).not.toContain("github.com/Liapoldus/core/internal/presentation/cli\"");
  });

  it("places SQLite bootstrap opening in the stores leaf", async () => {
    const cliSources = await directGoSources(cliRoot);
    const bootstrapSources = await readGoPackageSources(root, bootstrapPath).catch(() => "");

    expect(cliSources).not.toMatch(/^func\s+openBootstrapDatabase\s*\(/m);
    expect(bootstrapSources).toMatch(/^func\s+OpenDatabase\s*\(/m);
    expect(bootstrapSources).not.toContain("github.com/Liapoldus/core/internal/presentation/cli\"");
  });

  it("places plugin inventory presentation in the inventory leaf", async () => {
    const cliSources = await directGoSources(cliRoot);
    const bootstrapSources = await readGoPackageSources(root, bootstrapPath).catch(() => "");

    expect(cliSources).not.toMatch(/^func\s+presentPluginInventory\s*\(/m);
    expect(bootstrapSources).toMatch(/^func\s+PresentPluginInventory\s*\(/m);
    expect(bootstrapSources).not.toContain("github.com/Liapoldus/core/internal/presentation/cli\"");
  });

  it("places runtime service composition in the services leaf", async () => {
    const cliSources = await directGoSources(cliRoot);
    const bootstrapSources = await readGoPackageSources(root, bootstrapPath).catch(() => "");

    expect(cliSources).not.toMatch(/^func\s+newAdminMutationService\s*\(/m);
    expect(cliSources).not.toMatch(/^func\s+newGroupReleaseService\s*\(/m);
    expect(cliSources).not.toMatch(/^func\s+activateCurrentGroupRelease\s*\(/m);
    expect(cliSources).not.toMatch(/^func\s+cookiePolicyManagementService\s*\(/m);
    expect(bootstrapSources).toMatch(/^func\s+NewAdminMutationService\s*\(/m);
    expect(bootstrapSources).toMatch(/^func\s+NewGroupReleaseService\s*\(/m);
    expect(bootstrapSources).toMatch(/^func\s+ActivateCurrentGroupRelease\s*\(/m);
    expect(bootstrapSources).toMatch(/^func\s+CookiePolicyManagementService\s*\(/m);
    expect(bootstrapSources).not.toContain("github.com/Liapoldus/core/internal/presentation/cli\"");
  });
});
