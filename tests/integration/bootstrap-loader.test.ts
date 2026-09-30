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
        "    replicaServerCA: file:/run/secrets/plugin-replica-server-ca.crt",
        "plugins:",
        "  - instanceId: catalog",
        "    replicas:",
        "      - replicaId: catalog-a",
        "        endpoint: https://catalog-a.internal:9443",
        "        expectedPeerIdentity:",
        "          commonName: catalog-a",
        "          uniformResourceIdentifier: spiffe://liapoldus/plugin/catalog-a",
        "      - replicaId: catalog-b",
        "        endpoint: https://catalog-b.internal:9443",
        "        expectedPeerIdentity:",
        "          commonName: catalog-b",
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
        PluginReplicaServerCA: "/run/secrets/plugin-replica-server-ca.crt",
        Plugins: [
          {
            InstanceID: "catalog",
            Replicas: [
              {
                ReplicaID: "catalog-a",
                Endpoint: "https://catalog-a.internal:9443",
                ExpectedPeerIdentity: {
                  CommonName: "catalog-a",
                  UniformResourceIdentifier: "spiffe://liapoldus/plugin/catalog-a",
                },
              },
              {
                ReplicaID: "catalog-b",
                Endpoint: "https://catalog-b.internal:9443",
                ExpectedPeerIdentity: { CommonName: "catalog-b" },
              },
            ],
          },
        ],
      });

      expect(JSON.parse(result.stdout)).not.toMatchObject({
        ArtifactsPath: expect.any(String),
        ExecutionProfile: expect.any(String),
        PluginCatalogURL: expect.any(String),
      });

      // The declared registry is the only source of replica endpoints and
      // identities, so each of these must fail closed at load time. A registry
      // Core cannot interpret would otherwise surface later as a replica that
      // silently never converges, or as an identity Core failed to verify.
      const validRegistry = [
        "plugins:",
        "  - instanceId: catalog",
        "    replicas:",
        "      - replicaId: catalog-a",
        "        endpoint: https://catalog-a.internal:9443",
        "        expectedPeerIdentity:",
        "          commonName: catalog-a",
        "",
      ].join("\n");
      const rejectedRegistries: Record<string, string> = {
        "duplicate instance id": [
          "plugins:",
          "  - instanceId: catalog",
          "    replicas:",
          "      - replicaId: catalog-a",
          "        endpoint: https://catalog-a.internal:9443",
          "        expectedPeerIdentity:",
          "          commonName: catalog-a",
          "  - instanceId: catalog",
          "    replicas:",
          "      - replicaId: catalog-c",
          "        endpoint: https://catalog-c.internal:9443",
          "        expectedPeerIdentity:",
          "          commonName: catalog-c",
          "",
        ].join("\n"),
        "duplicate replica id": [
          "plugins:",
          "  - instanceId: catalog",
          "    replicas:",
          "      - replicaId: catalog-a",
          "        endpoint: https://catalog-a.internal:9443",
          "        expectedPeerIdentity:",
          "          commonName: catalog-a",
          "      - replicaId: catalog-a",
          "        endpoint: https://catalog-b.internal:9443",
          "        expectedPeerIdentity:",
          "          commonName: catalog-b",
          "",
        ].join("\n"),
        "non-https endpoint": validRegistry.replace("https://catalog-a.internal:9443", "http://catalog-a.internal:9443"),
        "endpoint with credentials": validRegistry.replace("https://catalog-a.internal:9443", "https://user:pass@catalog-a.internal:9443"),
        "empty replica set": [
          "plugins:",
          "  - instanceId: catalog",
          "    replicas: []",
          "",
        ].join("\n"),
        "missing plugins": "",
        "identity the SDK would refuse": validRegistry.replace("commonName: catalog-a", "commonName: '   '"),
      };
      for (const [reason, registry] of Object.entries(rejectedRegistries)) {
        const candidate = [
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
          "    replicaServerCA: file:/run/secrets/plugin-replica-server-ca.crt",
          registry,
        ].filter((line) => line !== "").join("\n") + "\n";
        await writeFile(coreConfig, candidate, "utf8");
        await expect(execFileAsync(binary, [coreConfig], { cwd: coreRoot }), reason).rejects.toBeDefined();
      }

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
