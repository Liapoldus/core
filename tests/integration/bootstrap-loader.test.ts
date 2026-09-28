import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("typed bootstrap loading", () => {
  it("validates and exposes only the bootstrap fields required to start Gateway", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-bootstrap-loader-"));
    const gatewayConfig = join(directory, "gateway.yaml");
    const binary = join(directory, "bootstrap-loader");
    try {
      await writeFile(gatewayConfig, [
        "state:",
        "  path: ./state/gateway.db",
        "artifacts:",
        "  path: ./state/artifacts",
        "execution:",
        "  profile: supervised",
        "management:",
        "  listen: 127.0.0.1:9443",
        "  tls:",
        "    certificate: file:/run/secrets/management.crt",
        "    key: file:/run/secrets/management.key",
        "pluginCatalog:",
        "  url: https://plugins.example.invalid/metadata",
        "",
      ].join("\n"), "utf8");
      await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/bootstrap-loader"], { cwd: coreRoot });
      const result = await execFileAsync(binary, [gatewayConfig], { cwd: coreRoot });

      expect(JSON.parse(result.stdout)).toMatchObject({
        StatePath: resolve(directory, "state/gateway.db"),
        ArtifactsPath: resolve(directory, "state/artifacts"),
        ManagementListen: "127.0.0.1:9443",
        ManagementCertificate: "/run/secrets/management.crt",
        ManagementKey: "/run/secrets/management.key",
        ExecutionProfile: "supervised",
        PluginCatalogURL: "https://plugins.example.invalid/metadata",
      });

      const legacy = [
        "state:",
        "  path: ./state/gateway.db",
        "artifacts:",
        "  path: ./state/artifacts",
        "management:",
        "  listen: 127.0.0.1:9443",
        "  tls:",
        "    certificate: file:/run/secrets/management.crt",
        "    key: file:/run/secrets/management.key",
        "execution:",
        "  profile: supervised",
        "pluginCatalog:",
        "  url: https://plugins.example.invalid/metadata",
        "legacyTrafficRuntime:",
        "  mode: embedded",
        "",
      ].join("\n");
      await writeFile(gatewayConfig, legacy, "utf8");
      await expect(execFileAsync(binary, [gatewayConfig], { cwd: coreRoot })).rejects.toBeDefined();

      const insecureRemote = legacy.replace("127.0.0.1:9443", "gateway.internal:9443").replace("listeners: {}\n", "");
      await writeFile(gatewayConfig, insecureRemote, "utf8");
      await expect(execFileAsync(binary, [gatewayConfig], { cwd: coreRoot })).rejects.toBeDefined();
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
