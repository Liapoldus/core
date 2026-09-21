import { describe, expect, it } from "vitest";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createConfig, createConfigDir } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

describe("gateway config explain", () => {
  it("reports listeners, routes, sites, and an unused-site issue", async () => {
    const directory = await createConfigDir();
    const root = join(directory, "gateway.yaml");
    await writeFile(
      root,
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes:\n" +
        "      - then:\n" +
        "          site: docs\n" +
        "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: directory\n" +
        "      root: ./docs\n" +
        "  assets:\n" +
        "    source:\n" +
        "      type: release\n" +
        "      slug: assets\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "explain", root]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result)).toEqual({
      ok: true,
      command: "config explain",
      report: {
        listeners: [{ name: "main", type: "http", address: ":8080" }],
        routes: [{ listener: "main", index: 1, site: "docs" }],
        sites: [
          { name: "assets", index: "index.html" },
          { name: "docs", index: "index.html" },
        ],
        issues: [{ kind: "unused", name: "assets" }],
      },
    });
  });

  it("flags a site that is redefined in an included document", async () => {
    const directory = await createConfigDir();
    const root = join(directory, "gateway.yaml");
    await writeFile(
      root,
      "includes:\n" +
        "  - sites.yaml\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes:\n" +
        "      - then:\n" +
        "          site: docs\n" +
        "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: directory\n" +
        "      root: ./root\n",
      "utf8",
    );
    await writeFile(
      join(directory, "sites.yaml"),
      "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: directory\n" +
        "      root: ./inc\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "explain", root]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result).report.issues).toEqual([
      { kind: "overridden", name: "docs" },
    ]);
  });

  it("rejects a route that targets an undefined site as an invalid configuration", async () => {
    const root = await createConfig(
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes:\n" +
        "      - then:\n" +
        "          site: missing\n",
    );

    const result = await runGateway(["--output", "json", "config", "explain", root]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({
      problem: { code: "config_invalid" },
    });
  });

  it("prints a human-readable report in text mode", async () => {
    const config = await createConfig(
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes:\n" +
        "      - then:\n" +
        "          site: docs\n" +
        "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: release\n" +
        "      slug: docs\n",
    );

    const result = await runGateway(["config", "explain"], {
      LIAPOLDUS_GATEWAY_CONFIG: config,
    });

    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain("listener main http :8080");
    expect(result.stdout).toContain("route main 1 -> site docs");
    expect(result.stdout).toContain("site docs index index.html");
    expect(result.stdout).not.toContain("issue unused docs");
  });
});