import { describe, expect, it } from "vitest";
import { createConfig } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

describe("generic plugin-backed auth policy configuration", () => {
  it("rejects removed built-in OIDC and JWT policy fields", async () => {
    const config = await createConfig("registry:\n  path: ./registry\nauthPolicies:\n  users:\n    oidc: {}\n    jwt: {}\n");
    const result = await runGateway(["--output", "json", "config", "validate", config]);
    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result).problem.code).toBe("config_invalid");
  }, 60_000);

  it("accepts an auth policy bound to an arbitrary declared capability", async () => {
    const config = await createConfig("registry:\n  path: ./registry\nplugins:\n  policy:\n    binary: ./policy\n    capabilities: [example.authenticate]\n    settings:\n      enabled: true\nauthPolicies:\n  users:\n    plugin:\n      instance: policy\n      capability: example.authenticate\nupstreams:\n  api:\n    targets: [{ address: 127.0.0.1:8080 }]\nlisteners:\n  web:\n    type: http\n    address: 127.0.0.1:0\n    routes:\n      - when: { path: /private }\n        then: { auth: users, proxy: { upstream: api } }\n");
    const result = await runGateway(["--output", "json", "config", "validate", config]);
    expect(result.exitCode, result.stdout + result.stderr).toBe(0);
    expect(jsonOutput(result).valid).toBe(true);
  }, 60_000);

  it("rejects a route that references an undefined auth policy", async () => {
    const config = await createConfig(`registry:\n  path: ./registry\nlisteners:\n  web:\n    type: http\n    address: 127.0.0.1:0\n    routes:\n      - when: { path: / }\n        then: { auth: missing }\n`);
    const result = await runGateway(["--output", "json", "config", "validate", config]);
    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result).problem.code).toBe("config_invalid");
  });

  it("rejects a plugin-owned subject rate-limit key", async () => {
    const rejected = await createConfig(
      "registry:\n  path: ./registry\nrateLimits:\n  subject: { key: identity-subject, requests: 10, per: 1m, burst: 2 }\n",
    );

    await expect(runGateway(["--output", "json", "config", "validate", rejected])).resolves.toMatchObject({ exitCode: 3 });
  });
});
