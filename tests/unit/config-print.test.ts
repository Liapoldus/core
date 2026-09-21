import { describe, expect, it } from "vitest";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createConfig, createConfigDir } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

describe("gateway config print", () => {
  it("prints an effective document: merges includes, drops the includes key, resolves variables, and keeps secrets as references", async () => {
    const directory = await createConfigDir();
    const root = join(directory, "gateway.yaml");
    await writeFile(
      root,
      "includes:\n" +
        "  - sites.yaml\n" +
        "variables:\n" +
        "  endpoint: 127.0.0.1\n" +
        "secrets:\n" +
        "  apiKey: env:API_KEY\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \"${endpoint}:8080\"\n" +
        "    routes:\n" +
        "      - then:\n" +
        "          site: docs\n" +
        "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: release\n" +
        "      slug: docs\n",
      "utf8",
    );
    await writeFile(
      join(directory, "sites.yaml"),
      "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: directory\n" +
        "      root: ./site\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "print", root]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result)).toEqual({
      ok: true,
      command: "config print",
      document: {
        variables: { endpoint: "127.0.0.1" },
        secrets: { apiKey: "env:API_KEY" },
        listeners: {
          main: {
            type: "http",
            address: "127.0.0.1:8080",
            routes: [{ then: { site: "docs" } }],
          },
        },
        sites: {
          docs: { source: { type: "directory", root: "./site" } },
        },
      },
    });
  });

  it("prints the effective document as YAML in text mode", async () => {
    const directory = await createConfigDir();
    const root = join(directory, "gateway.yaml");
    await writeFile(
      root,
      "includes:\n" +
        "  - sites.yaml\n" +
        "secrets:\n" +
        "  apiKey: env:API_KEY\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n",
      "utf8",
    );
    await writeFile(
      join(directory, "sites.yaml"),
      "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: directory\n" +
        "      root: ./site\n",
      "utf8",
    );

    const result = await runGateway(["config", "print", root]);

    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain("root: ./site");
    expect(result.stdout).toContain("apiKey: env:API_KEY");
    expect(result.stdout).toContain("address: \":8080\"");
    expect(result.stdout).not.toContain("includes:");
    expect(result.stdout).not.toContain("slug:");
  });

  it("resolves variables without leaking secret values, and only in non-secret fields", async () => {
    const config = await createConfig(
      "variables:\n" +
        "  host: 127.0.0.1\n" +
        "secrets:\n" +
        "  superSecret: env:SUPER_SECRET\n" +
        "listeners:\n" +
        "  http:\n" +
        "    type: http\n" +
        "    address: \"${host}:9000\"\n",
    );

    const result = await runGateway(["--output", "json", "config", "print"], {
      LIAPOLDUS_GATEWAY_CONFIG: config,
    });

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result).document).toEqual({
      variables: { host: "127.0.0.1" },
      secrets: { superSecret: "env:SUPER_SECRET" },
      listeners: {
        http: { type: "http", address: "127.0.0.1:9000" },
      },
    });
  });

  it("rejects a print target with an unresolved variable as an invalid configuration", async () => {
    const config = await createConfig(
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \"${missing}:8080\"\n" +
        "    routes: []\n",
    );

    const result = await runGateway(["--output", "json", "config", "print", config]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({
      problem: { code: "config_invalid" },
    });
  });
});