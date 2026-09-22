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
});
