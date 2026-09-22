import { describe, expect, it } from "vitest";
import { createConfig } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

describe("identity plugin configuration", () => {
  it("rejects removed built-in oidc and jwt policy fields", async () => {
    const config = await createConfig("registry:\n  path: ./registry\nauthPolicies:\n  users:\n    oidc: {}\n");
    const result = await runGateway(["--output", "json", "config", "validate", config]);
    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result).problem.code).toBe("config_invalid");
  });

  it("accepts an auth policy bound to a plugin capability and inline settings", async () => {
    const config = await createConfig("registry:\n  path: ./registry\nplugins:\n  identity:\n    binary: ./identity\n    capabilities: [identity.client.authenticate]\n    settings:\n      clients: {}\nauthPolicies:\n  users:\n    plugin:\n      instance: identity\n      capability: identity.client.authenticate\n");
    const result = await runGateway(["--output", "json", "config", "validate", config]);
    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result).valid).toBe(true);
  });

  it("uses the normalized identity subject rather than a token-format-specific rate-limit key", async () => {
    const accepted = await createConfig(
      "registry:\n  path: ./registry\nrateLimits:\n  identity: { key: identity-subject, requests: 10, per: 1m, burst: 2 }\n",
    );
    const rejected = await createConfig(
      "registry:\n  path: ./registry\nrateLimits:\n  legacy: { key: jwt-subject, requests: 10, per: 1m, burst: 2 }\n",
    );

    await expect(runGateway(["--output", "json", "config", "validate", accepted])).resolves.toMatchObject({ exitCode: 0 });
    await expect(runGateway(["--output", "json", "config", "validate", rejected])).resolves.toMatchObject({ exitCode: 3 });
  });
});
