import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("architecture lint boundaries", () => {
  it("keeps infrastructure adapters explicit about their vendor dependencies", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");

    expect(architecture).toMatch(/yaml:\s*\{ in: \[gopkg\.in\/yaml\.v3\] \}/);
    expect(architecture).toMatch(/textNormalization:\s*\{ in: \[golang\.org\/x\/text\/cases, golang\.org\/x\/text\/unicode\/norm\] \}/);
    expect(architecture).toMatch(/infrastructureArtifacts:[\s\S]*?canUse: \[[^\]]*yaml[^\]]*textNormalization[^\]]*\]/);
    expect(architecture).toMatch(/infrastructureCaddy:[\s\S]*?canUse: \[[^\]]*pluginprotocol[^\]]*caddyLayer4[^\]]*websocket[^\]]*\]/);
  });

  it("does not let storage depend on config or CLI depend on data-plane/protocol adapters", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");
    const storage = await readFile(join(root, "internal/infrastructure/storage/sqlite_plugin_instances.go"), "utf8");
    const cli = await readFile(join(root, "internal/presentation/cli/serve_bootstrap.go"), "utf8");

    expect(architecture).toMatch(/infrastructureStorage:[\s\S]*?mayDependOn: \[[^\]]*\]/);
    const storageDependencies = architecture.match(/infrastructureStorage:[\s\S]*?mayDependOn: \[([^\]]*)\]/)?.[1] ?? "";
    expect(storageDependencies).not.toContain("infrastructureConfig");

    expect(architecture).toMatch(/presentationCLI:[\s\S]*?mayDependOn: \[[^\]]*\]/);
    const cliDependencies = architecture.match(/presentationCLI:[\s\S]*?mayDependOn: \[([^\]]*)\]/)?.[1] ?? "";
    expect(cliDependencies).not.toContain("infrastructureCaddy");
    expect(cliDependencies).not.toContain("pluginprotocol");
    expect(storage).not.toContain("internal/infrastructure/config");
    expect(cli).not.toContain("internal/infrastructure/caddy");
    expect(cli).not.toContain("github.com/Liapoldus/pluginprotocol");
  });
});
