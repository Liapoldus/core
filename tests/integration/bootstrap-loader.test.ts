import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("typed bootstrap loading", () => {
  it("validates and exposes only the bootstrap fields required to start Core", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-bootstrap-loader-"));
    const coreConfig = join(directory, "core.yaml");
    const binary = join(directory, "bootstrap-loader");
    try {
      await writeFile(coreConfig, [
        "state:",
        "  path: ./state/core.db",
        "management:",
        "  listen: 127.0.0.1:9443",
        "  tls:",
        "    certificate: file:/run/secrets/management.crt",
        "    key: file:/run/secrets/management.key",
        "pluginControl:",
        "  listen: 127.0.0.1:9444",
        "  publicURL: https://core.internal:9444",
        "  tls:",
        "    certificate: file:/run/secrets/plugin-control.crt",
        "    key: file:/run/secrets/plugin-control.key",
        "    replicaClientCA: file:/run/secrets/plugin-replica-ca.crt",
        "",
      ].join("\n"), "utf8");
      await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/bootstrap-loader"], { cwd: coreRoot });
      const result = await execFileAsync(binary, [coreConfig], { cwd: coreRoot });

      expect(JSON.parse(result.stdout)).toMatchObject({
        StatePath: resolve(directory, "state/core.db"),
        ManagementListen: "127.0.0.1:9443",
        ManagementCertificate: "/run/secrets/management.crt",
        ManagementKey: "/run/secrets/management.key",
        PluginControlListen: "127.0.0.1:9444",
        PluginControlPublicURL: "https://core.internal:9444",
        PluginControlCertificate: "/run/secrets/plugin-control.crt",
        PluginControlKey: "/run/secrets/plugin-control.key",
        PluginReplicaClientCA: "/run/secrets/plugin-replica-ca.crt",
      });

      expect(JSON.parse(result.stdout)).not.toMatchObject({
        ArtifactsPath: expect.any(String),
        ExecutionProfile: expect.any(String),
        PluginCatalogURL: expect.any(String),
      });

      const legacy = [
        "state:",
        "  path: ./state/core.db",
        "management:",
        "  listen: 127.0.0.1:9443",
        "  tls:",
        "    certificate: file:/run/secrets/management.crt",
        "    key: file:/run/secrets/management.key",
        "pluginControl:",
        "  listen: 127.0.0.1:9444",
        "  publicURL: https://core.internal:9444",
        "  tls:",
        "    certificate: file:/run/secrets/plugin-control.crt",
        "    key: file:/run/secrets/plugin-control.key",
        "    replicaClientCA: file:/run/secrets/plugin-replica-ca.crt",
        "legacyTrafficRuntime:",
        "  mode: embedded",
        "",
      ].join("\n");
      await writeFile(coreConfig, legacy, "utf8");
      await expect(execFileAsync(binary, [coreConfig], { cwd: coreRoot })).rejects.toBeDefined();

      const insecureRemote = legacy.replace("127.0.0.1:9443", "core.internal:9443").replace("listeners: {}\n", "");
      await writeFile(coreConfig, insecureRemote, "utf8");
      await expect(execFileAsync(binary, [coreConfig], { cwd: coreRoot })).rejects.toBeDefined();
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
