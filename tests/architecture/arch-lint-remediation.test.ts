import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

describe("architecture lint boundaries", () => {
  it("keeps infrastructure adapters explicit about their vendor dependencies", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");
    const dependencies = architecture.slice(architecture.indexOf("deps:"));

    expect(architecture).toMatch(/yaml:\s*\{ in: \[gopkg\.in\/yaml\.v3\] \}/);
    expect(architecture).toMatch(/textNormalization:\s*\{ in: \[golang\.org\/x\/text\/cases, golang\.org\/x\/text\/unicode\/norm\] \}/);
    expect(architecture).toMatch(/caddyfile:\s*\{ in: \[github\.com\/caddyserver\/caddy\/v2\/caddyconfig\/caddyfile\] \}/);
    expect(architecture).toMatch(/caddyLayer4:\s*\{ in: \[github\.com\/mholt\/caddy-l4, github\.com\/mholt\/caddy-l4\/layer4\] \}/);
    expect(architecture).toMatch(/protobuf:\s*\{ in: \[google\.golang\.org\/protobuf\/proto, google\.golang\.org\/protobuf\/encoding\/protojson\] \}/);
    expect(architecture).toMatch(/infrastructureArtifacts:[\s\S]*?canUse: \[[^\]]*yaml[^\]]*textNormalization[^\]]*\]/);
    expect(dependencies.match(/infrastructureArtifacts:[\s\S]*?mayDependOn: \[([^\]]*)\]/)?.[1]).toContain("contractAdapter");
    const caddyDependencies = dependencies.match(/infrastructureCaddy:[\s\S]*?canUse: \[([^\]]*)\]/)?.[1] ?? "";
    expect(caddyDependencies).toContain("pluginprotocol");
    expect(caddyDependencies).toContain("caddyLayer4");
    expect(caddyDependencies).toContain("websocket");
  });

  it("does not let storage depend on config or CLI depend on data-plane/protocol adapters", async () => {
    const architecture = await readFile(join(root, ".go-arch-lint.yml"), "utf8");
    const dependencies = architecture.slice(architecture.indexOf("deps:"));
    const storage = await readFile(join(root, "internal/infrastructure/storage/sqlite_plugin_instances.go"), "utf8");
    const cli = await readFile(join(root, "internal/presentation/cli/serve_bootstrap.go"), "utf8");

    expect(dependencies).toMatch(/infrastructureStorage:[\s\S]*?mayDependOn: \[[^\]]*\]/);
    const storageDependencies = dependencies.match(/infrastructureStorage:[\s\S]*?mayDependOn: \[([^\]]*)\]/)?.[1] ?? "";
    expect(storageDependencies).not.toContain("infrastructureConfig");

    expect(dependencies).toMatch(/presentationCLI:[\s\S]*?mayDependOn: \[[^\]]*\]/);
    const cliDependencies = dependencies.match(/presentationCLI:[\s\S]*?mayDependOn: \[([^\]]*)\]/)?.[1] ?? "";
    expect(cliDependencies).not.toContain("infrastructureCaddy");
    expect(cliDependencies).not.toContain("pluginprotocol");
    expect(storage).not.toContain("internal/infrastructure/config");
    expect(cli).not.toContain("internal/infrastructure/caddy");
    expect(cli).not.toContain("github.com/Liapoldus/pluginprotocol");
  });
});
