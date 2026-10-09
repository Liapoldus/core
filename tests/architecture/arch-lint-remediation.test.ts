import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { readGoPackageSources } from "../support/presentation-source";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("architecture lint boundaries", () => {
  it("defines explicit boundaries for the planned presentation subpackages", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");
    const dependencies = architecture.slice(architecture.indexOf("deps:"));
    const packages = [
      ["presentationAPIHandlers", "internal/presentation/api/handlers"],
      ["runtime", "internal/runtime"],
    ];

    for (const [component, directory] of packages) {
      expect(architecture).toMatch(new RegExp(`${component}: \\{ in: ${directory.replaceAll("/", "\\/")} \\}`));
      const block = dependencies.match(new RegExp(`${component}:[\\s\\S]*?(?=\\n  [\\w]+:|$)`))?.[0] ?? "";
      expect(block, `${component} must define mayDependOn`).toMatch(/mayDependOn: \[[^\]]*\]/);
      expect(block, `${component} must define canUse`).toMatch(/canUse: \[[^\]]*\]/);
      expect(block).not.toContain("pluginprotocol");
    }
  });

  it("allows API root to depend on handlers but forbids the reverse edge", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");
    const dependencies = architecture.slice(architecture.indexOf("deps:"));
    const apiDependencies = dependencies.match(/presentationAPI:[\s\S]*?mayDependOn: \[([^\]]*)\]/)?.[1] ?? "";
    const handlerDependencies = dependencies.match(/presentationAPIHandlers:[\s\S]*?mayDependOn: \[([^\]]*)\]/)?.[1] ?? "";

    expect(apiDependencies).toContain("presentationAPIHandlers");
    expect(handlerDependencies).not.toContain("presentationAPI");
  });

  it("keeps generic infrastructure adapters explicit about their vendor dependencies", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");

    expect(architecture).toMatch(/yaml:\s*\{ in: \[gopkg\.in\/yaml\.v3\] \}/);
    expect(architecture).toMatch(/textNormalization:\s*\{ in: \[golang\.org\/x\/text\/cases, golang\.org\/x\/text\/unicode\/norm\] \}/);
    expect(architecture).not.toMatch(/protobuf:\s*\{/);
    expect(architecture).toMatch(/pluginSDK:\s*\{ in: \[github\.com\/Liapoldus\/plugin-sdk,/);
  });

  it("does not let storage depend on config or CLI depend on protocol adapters", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");
    const dependencies = architecture.slice(architecture.indexOf("deps:"));
    const storage = await readFile(join(root, "internal/infrastructure/storage/sqlite_plugin_instances.go"), "utf8");
    const runtime = await readGoPackageSources(root, "internal/runtime");

    expect(dependencies).toMatch(/infrastructureStorage:[\s\S]*?mayDependOn: \[[^\]]*\]/);
    const storageDependencies = dependencies.match(/infrastructureStorage:[\s\S]*?mayDependOn: \[([^\]]*)\]/)?.[1] ?? "";
    expect(storageDependencies).not.toContain("infrastructureConfig");

    expect(dependencies).toMatch(/runtime:[\s\S]*?mayDependOn: \[[^\]]*\]/);
    const runtimeDependencies = dependencies.match(/runtime:[\s\S]*?mayDependOn: \[([^\]]*)\]/)?.[1] ?? "";
    expect(runtimeDependencies).not.toContain("pluginprotocol");
    expect(storage).not.toContain("internal/infrastructure/config");
    expect(runtime).not.toContain("github.com/Liapoldus/pluginprotocol");
  });
});
