import { describe, expect, it } from "vitest";
import { createConfig } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

const bootstrap = `state:
  path: ./state/gateway.db
artifacts:
  path: ./state/artifacts
management:
  listen: 127.0.0.1:9443
  tls:
    certificate: file:/run/secrets/management.crt
    key: file:/run/secrets/management.key
caddy:
  variant: embedded
`;

describe("minimal Caddy-native Gateway bootstrap", () => {
  it("accepts the minimal bootstrap without traffic route declarations", async () => {
    const config = await createConfig(bootstrap);
    const result = await runGateway(["--output", "json", "--config", config, "config", "validate"]);

    expect(result.exitCode, result.stdout).toBe(0);
    expect(jsonOutput(result)).toMatchObject({ ok: true, valid: true });
  });

  it.each([
    ["listeners", "listeners: {}\n"],
    ["routes", "routes: []\n"],
    ["includes", "includes: [./parts/*.yaml]\n"],
    ["site.yaml", "sites:\n  app:\n    source:\n      type: directory\n"],
  ])("rejects legacy traffic configuration: %s", async (_name, legacyFields) => {
    const config = await createConfig(`${bootstrap}${legacyFields}`);
    const result = await runGateway(["--output", "json", "--config", config, "config", "validate"]);

    expect(result.exitCode).not.toBe(0);
    expect(jsonOutput(result)).toHaveProperty("problem.code");
  });

  it("rejects plaintext secrets in bootstrap and requires external references", async () => {
    const config = await createConfig(bootstrap.replace("file:/run/secrets/management.crt", "plain-secret-value"));
    const result = await runGateway(["--output", "json", "--config", config, "config", "validate"]);

    expect(result.exitCode).not.toBe(0);
    expect(result.stdout).not.toContain("plain-secret-value");
  });

  it("rejects the removed bearer verifier file from bootstrap", async () => {
    const config = await createConfig(bootstrap.replace("caddy:\n", "  bearerVerifier: file:./secrets/service-keys.json\ncaddy:\n"));
    const result = await runGateway(["--output", "json", "--config", config, "config", "validate"]);

    expect(result.exitCode).not.toBe(0);
    expect(jsonOutput(result)).toHaveProperty("problem.code");
  });
});
