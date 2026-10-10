import { readdir, readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const presentation = join(root, "internal", "presentation");

async function directGoFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  return entries
    .filter((entry) => entry.isFile() && entry.name.endsWith(".go"))
    .map((entry) => join(directory, entry.name))
    .sort();
}

async function nestedDirectories(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const children = entries.filter((entry) => entry.isDirectory());
  const descendants = await Promise.all(
    children.map((entry) => nestedDirectories(join(directory, entry.name))),
  );
  return [...children.map((entry) => join(directory, entry.name)), ...descendants.flat()];
}

async function recursiveGoFiles(directory: string): Promise<string[]> {
  const [files, directories] = await Promise.all([
    directGoFiles(directory),
    nestedDirectories(directory),
  ]);
  const nestedFiles = await Promise.all(directories.map(directGoFiles));
  return [...files, ...nestedFiles.flat()].sort();
}

describe("presentation package ownership", () => {
  it("exports only Server from the API root package", async () => {
    const files = await directGoFiles(join(presentation, "api"));
    const sources = await Promise.all(files.map((file) => readFile(file, "utf8")));
    const exportedTypes = sources
      .join("\n")
      .match(/^type\s+([A-Z][A-Za-z0-9_]*)\s/gm)
      ?.map((declaration) => declaration.trim().split(/\s+/)[1]) ?? [];

    expect(exportedTypes).toEqual(["Server"]);
  });

  it("freezes the current Server field names and order", async () => {
    const files = await directGoFiles(join(presentation, "api"));
    const sources = await Promise.all(files.map((file) => readFile(file, "utf8")));
    const server = sources.join("\n").match(/type Server struct \{([\s\S]*?)\n\}/)?.[1];
    if (!server) throw new Error("api.Server declaration was not found");

    const fields = server
      .split("\n")
      .map((line) => line.trim().match(/^([A-Za-z_][A-Za-z0-9_]*)\s+/)?.[1])
      .filter((name): name is string => name !== undefined);

    expect(fields).toEqual([
      "CoreSettings",
      "ValidateSettings",
      "Token",
      "ServiceAccounts",
      "mu",
      "Operations",
      "ConfigBundles",
      "PluginAdminControl",
      "PluginLinks",
      "TrafficRollouts",
      "TrafficRolloutAPI",
      "Plugins",
      "PluginIDField",
      "Audit",
      "AccessService",
      "DataPlaneState",
      "DataPlaneReason",
      "DataPlaneReadiness",
      "DataPlaneDrift",
      "AuditWords",
      "Management",
      "Errors",
      "TLSConfig",
      "contractOnce",
    ]);
    expect(fields.filter((name) => name[0] === name[0].toUpperCase())).toHaveLength(22);
    expect(fields.filter((name) => name[0] === name[0].toLowerCase())).toHaveLength(2);
  });

  it("requires each nested Go package directory to contain at least two files", async () => {
    const roots = [join(presentation, "api")];
    const directories = (await Promise.all(roots.map(nestedDirectories))).flat();

    for (const directory of directories) {
      const files = await directGoFiles(directory);
      if (files.length > 0) {
        expect(files.length, `${directory} must not be a one-file package`).toBeGreaterThanOrEqual(2);
      }
    }
  });

  it("keeps testing dependencies out of presentation runtime sources", async () => {
    const files = await recursiveGoFiles(presentation);
    for (const file of files.filter((path) => !path.endsWith("_test.go"))) {
      expect(await readFile(file, "utf8"), file).not.toMatch(/"testing"/);
    }
  });

  it("declares acyclic API dependency directions", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");
    const dependencies = architecture.slice(architecture.indexOf("deps:"));
    const mayDependOn = (component: string): string[] => {
      const block = dependencies.match(new RegExp(`${component}:[\\s\\S]*?mayDependOn: \\[([^\\]]*)\\]`))?.[1] ?? "";
      return block.split(",").map((dependency) => dependency.trim()).filter(Boolean);
    };

    expect(mayDependOn("presentationAPI")).toContain("presentationAPIHandlers");
    expect(mayDependOn("presentationAPIHandlers")).not.toContain("presentationAPI");
    expect(mayDependOn("runtime")).toContain("presentationAPI");
    expect(mayDependOn("runtime")).not.toContain("presentationAPIHandlers");
  });
});
