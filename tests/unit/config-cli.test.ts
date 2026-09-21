import { describe, expect, it } from "vitest";
import { createConfig } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

describe("gateway config CLI", () => {
  it("uses --config before every other discovery source", async () => {
    const config = await createConfig("registry:\n  path: ./registry\n");

    const result = await runGateway(
      ["--output", "json", "--config", config, "config", "path"],
      { LIAPOLDUS_GATEWAY_CONFIG: "/must-not-be-used/gateway.yaml" },
    );

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result)).toMatchObject({
      ok: true,
      command: "config path",
      path: config,
      source: "flag",
    });
  });

  it("validates a minimal configured registry without mutating runtime", async () => {
    const config = await createConfig("registry:\n  path: ./registry\nlisteners: {}\n");
    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result)).toMatchObject({
      ok: true,
      command: "config validate",
      valid: true,
    });
  });

  it("returns config_not_found with documented exit code when no source exists", async () => {
    const result = await runGateway(
      ["--output", "json", "config", "path"],
      { LIAPOLDUS_GATEWAY_CONFIG: "/does-not-exist/gateway.yaml", LIAPOLDUS_CONFIG_DIR: "/does-not-exist" },
    );

    expect(result.exitCode).toBe(2);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_not_found" } });
  });

  it("rejects unknown root fields as a validation error", async () => {
    const config = await createConfig("registry:\n  path: ./registry\nnotAContractField: true\n");
    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "unknown_field" } });
  });
});
