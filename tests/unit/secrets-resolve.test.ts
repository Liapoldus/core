import { describe, expect, it } from "vitest";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createConfigDir } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

describe("gateway config secrets", () => {
  it("rejects a file: secret whose file does not exist as an invalid configuration", async () => {
    const directory = await createConfigDir();
    const config = join(directory, "gateway.yaml");
    await writeFile(
      config,
      "secrets:\n" +
        "  token: file:" +
        join(directory, "absent.txt") +
        "\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "validate", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_invalid" } });
  });

  it("reads a file: secret without leaking its contents in print output", async () => {
    const directory = await createConfigDir();
    const secretPath = join(directory, "credential.txt");
    await writeFile(secretPath, "hunter2-secret\n", "utf8");
    const config = join(directory, "gateway.yaml");
    await writeFile(
      config,
      "secrets:\n" +
        "  token: file:" +
        secretPath +
        "\n" +
        "variables:\n" +
        "  host: 127.0.0.1\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \"${host}:8080\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const validate = await runGateway(["--output", "json", "config", "validate", config]);

    expect(validate.exitCode).toBe(0);
    expect(jsonOutput(validate)).toMatchObject({ ok: true, valid: true });

    const print = await runGateway(["--output", "json", "config", "print", config]);

    expect(print.exitCode).toBe(0);
    expect(jsonOutput(print).document).toEqual({
      secrets: { token: "file:" + secretPath },
      variables: { host: "127.0.0.1" },
      listeners: { main: { type: "http", address: "127.0.0.1:8080", routes: [] } },
    });
    expect(print.stdout).not.toContain("hunter2-secret");
  });

  it("accepts an env: secret and never prints its resolved value", async () => {
    const config = await createConfigDir();
    const path = join(config, "gateway.yaml");
    await writeFile(
      path,
      "secrets:\n" +
        "  apiKey: env:GATEWAY_TEST_TOKEN\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "print", path], {
      GATEWAY_TEST_TOKEN: "env-viewer-token-value",
    });

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result).document).toEqual({
      secrets: { apiKey: "env:GATEWAY_TEST_TOKEN" },
      listeners: { main: { type: "http", address: ":8080", routes: [] } },
    });
    expect(result.stdout).not.toContain("env-viewer-token-value");
  });
});