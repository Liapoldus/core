import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
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
        "management:",
        "  listen: 127.0.0.1:9443",
        "  tls:",
        "    certificate: file:/run/secrets/management.crt",
        "    key: file:/run/secrets/management.key",
        "  bearerVerifier: file:/run/secrets/service-keys.json",
        "caddy:",
        "  variant: external",
        "  binary: /usr/local/bin/liapoldus-caddy",
        "  expectedBuildID: caddy-module-set-1",
        "",
      ].join("\n"), "utf8");
      await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/bootstrap-loader"], { cwd: coreRoot });
      const result = await execFileAsync(binary, [gatewayConfig], { cwd: coreRoot });

      expect(JSON.parse(result.stdout)).toMatchObject({
        StatePath: "./state/gateway.db",
        ArtifactsPath: "./state/artifacts",
        ManagementListen: "127.0.0.1:9443",
        ManagementCertificate: "file:/run/secrets/management.crt",
        ManagementKey: "file:/run/secrets/management.key",
        ManagementBearerVerifier: "file:/run/secrets/service-keys.json",
        CaddyVariant: "external",
        CaddyBinary: "/usr/local/bin/liapoldus-caddy",
        CaddyExpectedBuildID: "caddy-module-set-1",
      });
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
});
