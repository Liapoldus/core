import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

describe("legacy bootstrap migration input", () => {
  it("validates migration data and reports settings without preserving static membership", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-core-migration-input-"));
    const coreConfig = join(directory, "core.yaml");
    const binary = join(directory, "core-migrate");
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
      await execFileAsync("go", ["build", "-o", binary, "./cmd/core-migrate"], { cwd: coreRoot });
      const result = await execFileAsync(binary, ["--input", coreConfig, "--dry-run"], { cwd: coreRoot });

      expect(JSON.parse(result.stdout)).toMatchObject({
        schemaVersion: 1,
        settings: {
          management: {
            listen: "127.0.0.1:9443",
            tls: {
              certificate: "/run/secrets/management.crt",
              key: "/run/secrets/management.key",
            },
          },
          pluginControl: {
            listen: "127.0.0.1:9444",
            publicURL: "https://core.internal:9444",
            tls: {
              certificate: "/run/secrets/plugin-control.crt",
              key: "/run/secrets/plugin-control.key",
            },
            replicaClientCA: "/run/secrets/plugin-replica-ca.crt",
            replicaServerCA: "/run/secrets/plugin-replica-server-ca.crt",
          },
          secretRoot: directory,
        },
        ignoredStaticPluginInstances: ["catalog"],
      });

      // The retired static registry is not migrated. Invalid or ambiguous
      // declarations must still be rejected rather than silently discarded.
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
        await expect(execFileAsync(binary, ["--input", coreConfig, "--dry-run"], { cwd: coreRoot }), reason).rejects.toBeDefined();
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
      await expect(execFileAsync(binary, ["--input", coreConfig, "--dry-run"], { cwd: coreRoot })).rejects.toBeDefined();

      const insecureRemote = legacy.replace("127.0.0.1:9443", "core.internal:9443").replace("listeners: {}\n", "");
      await writeFile(coreConfig, insecureRemote, "utf8");
      await expect(execFileAsync(binary, ["--input", coreConfig, "--dry-run"], { cwd: coreRoot })).rejects.toBeDefined();
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
