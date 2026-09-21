import { describe, expect, it } from "vitest";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createConfigDir } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

describe("gateway config includes globs", () => {
  it("expands an include glob to every match, merges distinct keys across files, and applies files in lexicographic order", async () => {
    const directory = await createConfigDir();
    await mkdir(join(directory, "conf.d"));
    const root = join(directory, "gateway.yaml");

    await writeFile(
      join(directory, "conf.d", "omega.yaml"),
      "secrets:\n" +
        "  apiKey: env:API_KEY\n" +
        "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: directory\n" +
        "      root: ./omega\n",
      "utf8",
    );
    await writeFile(
      join(directory, "conf.d", "alpha.yaml"),
      "variables:\n" +
        "  host: 127.0.0.1\n" +
        "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: directory\n" +
        "      root: ./alpha\n",
      "utf8",
    );
    await writeFile(
      root,
      "includes:\n" +
        "  - conf.d/*.yaml\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \"${host}:8080\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "print", root]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result).document).toEqual({
      variables: { host: "127.0.0.1" },
      secrets: { apiKey: "env:API_KEY" },
      listeners: { main: { type: "http", address: "127.0.0.1:8080", routes: [] } },
      sites: { docs: { source: { type: "directory", root: "./omega" } } },
    });
  });

  it("recurses a ** glob through subdirectories and applies matches deepest-lexicographically-last", async () => {
    const directory = await createConfigDir();
    await mkdir(join(directory, "conf.d", "prod"), { recursive: true });
    await mkdir(join(directory, "conf.d", "dev"), { recursive: true });
    const root = join(directory, "gateway.yaml");

    await writeFile(
      join(directory, "conf.d", "prod", "common.yaml"),
      "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: directory\n" +
        "      root: ./prod\n",
      "utf8",
    );
    await writeFile(
      join(directory, "conf.d", "dev", "common.yaml"),
      "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: directory\n" +
        "      root: ./dev\n",
      "utf8",
    );
    await writeFile(
      root,
      "includes:\n" +
        "  - \"**/common.yaml\"\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "print", root]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result).document).toEqual({
      listeners: { main: { type: "http", address: ":8080", routes: [] } },
      sites: { docs: { source: { type: "directory", root: "./prod" } } },
    });
  });

  it("rejects an include glob that expands back onto the declaring file as a cycle", async () => {
    const directory = await createConfigDir();
    const root = join(directory, "gateway.yaml");
    await writeFile(
      root,
      "includes:\n" +
        "  - \"*.yaml\"\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "print", root]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_invalid" } });
  });

  it("rejects an include glob with no matches as an invalid configuration", async () => {
    const directory = await createConfigDir();
    const root = join(directory, "gateway.yaml");
    await writeFile(
      root,
      "includes:\n" +
        "  - missing/*.yaml\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const result = await runGateway(["--output", "json", "config", "print", root]);

    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_invalid" } });
  });
});