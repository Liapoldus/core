import { readFileSync } from "node:fs";
import { basename, dirname, join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { createConfig } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

const cliPathVectors = (() => {
  const source = readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8");
  const contract = JSON.parse(source) as {
    vectors: Array<{
      id: string;
      input: { env?: Record<string, string>; files?: string[] };
      expected: { configPath?: string; source?: string };
    }>;
  };
  return contract.vectors;
})();

function cliPathVector(id: string) {
  const match = cliPathVectors.find((candidate) => candidate.id === id);
  if (!match) throw new Error(`${id} golden vector is missing`);
  return match;
}

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

  it("reports the environment variable as the selected config source", async () => {
    const vector = cliPathVector("cli-config-discovery");
    const config = await createConfig("registry:\n  path: ./registry\n");
    const environmentVariable = Object.keys(vector.input.env ?? {})[0];
    const expectedFile = basename(vector.expected.configPath ?? "");
    if (!environmentVariable || !expectedFile) throw new Error("cli-config-discovery vector is incomplete");
    const configDirectory = dirname(config);
    const result = await runGateway(["--output", "json", "config", "path"], {
      [environmentVariable]: configDirectory,
    });

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result)).toMatchObject({
      path: join(configDirectory, expectedFile),
      source: vector.expected.source,
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
    const vector = cliPathVector("cli-config-missing");
    expect(vector.input.files).toEqual([]);
    const result = await runGateway(
      ["--output", "json", "config", "path"],
      { LIAPOLDUS_GATEWAY_CONFIG: "/does-not-exist/gateway.yaml", LIAPOLDUS_CONFIG_DIR: "/does-not-exist" },
    );

    expect(result.exitCode).toBe(vector.expected.exit);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: vector.expected.code } });
  });

  it("rejects unknown root fields as a validation error", async () => {
    const config = await createConfig("registry:\n  path: ./registry\nnotAContractField: true\n");
    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "unknown_field" } });
  });
});
