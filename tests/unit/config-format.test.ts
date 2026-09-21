import { describe, expect, it } from "vitest";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createConfig, createConfigDir } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

describe("gateway config format", () => {
  it("rewrites a config file in place with canonical formatting and is idempotent", async () => {
    const directory = await createConfigDir();
    const root = join(directory, "gateway.yaml");
    await writeFile(
      root,
      "listeners:\n" +
        "  http:\n" +
        "    routes: []\n" +
        "    address: \":8080\"\n" +
        "    type: http\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "format", root]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result)).toEqual({
      ok: true,
      command: "config format",
      path: root,
    });

    const formatted = await readFile(root, "utf8");
    expect(formatted.indexOf("address:")).toBeLessThan(formatted.indexOf("routes:"));
    expect(formatted.indexOf("routes:")).toBeLessThan(formatted.indexOf("type:"));

    const second = await runGateway(["config", "format", root]);
    expect(second.exitCode).toBe(0);
    expect(await readFile(root, "utf8")).toBe(formatted);
  });

  it("expands YAML anchors into duplicate values in canonical output", async () => {
    const root = await createConfig(
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    tls: &tlsProfile\n" +
        "      mode: terminate\n" +
        "    routes: []\n" +
        "  extra:\n" +
        "    type: http\n" +
        "    address: \":8081\"\n" +
        "    tls: *tlsProfile\n" +
        "    routes: []\n",
    );

    const result = await runGateway(["config", "format", root]);

    expect(result.exitCode).toBe(0);
    const formatted = await readFile(root, "utf8");
    expect(formatted).not.toContain("&tlsProfile");
    expect(formatted.split("mode: terminate").length - 1).toBe(2);
  });

  it("rejects formatting a config with an unknown root field", async () => {
    const config = await createConfig("notAContractField: true\n");

    const result = await runGateway(["--output", "json", "config", "format", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({
      problem: { code: "unknown_field" },
    });
  });
});