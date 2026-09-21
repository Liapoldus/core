import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { jsonOutput, runGateway } from "../support/gateway.js";

const invalidIncludeConfig = fileURLToPath(
  new URL("../fixtures/e2e/config-includes/gateway.yaml", import.meta.url),
);
const unresolvedVariableConfig = fileURLToPath(
  new URL("../fixtures/e2e/unresolved-variable/gateway.yaml", import.meta.url),
);

describe("Gateway YAML end-to-end", () => {
  it("validates included YAML files as one configuration graph", async () => {
    const result = await runGateway([
      "--output",
      "json",
      "config",
      "validate",
      invalidIncludeConfig,
    ]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({
      problem: { code: "unknown_field" },
    });
  });

  it("rejects an unresolved variable after reading the complete YAML graph", async () => {
    const result = await runGateway([
      "--output",
      "json",
      "config",
      "validate",
      unresolvedVariableConfig,
    ]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({
      problem: { code: "config_invalid" },
    });
  });
});
