import { describe, expect, it } from "vitest";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createConfigDir } from "../support/fixture.js";
import { jsonOutput, runGateway } from "../support/gateway.js";

describe("gateway config diff", () => {
  it("reports no changes for identical configurations", async () => {
    const directory = await createConfigDir();
    await writeFile(
      join(directory, "a.yaml"),
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n",
      "utf8",
    );
    await writeFile(
      join(directory, "b.yaml"),
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const result = await runGateway([
      "--output",
      "json",
      "config",
      "diff",
      join(directory, "a.yaml"),
      join(directory, "b.yaml"),
    ]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result).diff).toEqual({
      added: [],
      removed: [],
      changed: [],
    });
  });

  it("reports an added listener between two configurations", async () => {
    const directory = await createConfigDir();
    await writeFile(
      join(directory, "a.yaml"),
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n",
      "utf8",
    );
    await writeFile(
      join(directory, "b.yaml"),
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n" +
        "  web:\n" +
        "    type: http\n" +
        "    address: \":9000\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const result = await runGateway([
      "--output",
      "json",
      "config",
      "diff",
      join(directory, "a.yaml"),
      join(directory, "b.yaml"),
    ]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result).diff).toEqual({
      added: [{ section: "listeners", name: "web" }],
      removed: [],
      changed: [],
    });
  });

  it("reports removed sites and changed listeners, with a text report", async () => {
    const directory = await createConfigDir();
    await writeFile(
      join(directory, "a.yaml"),
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":8080\"\n" +
        "    routes: []\n" +
        "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: release\n" +
        "      slug: docs\n" +
        "  legacy:\n" +
        "    source:\n" +
        "      type: release\n" +
        "      slug: legacy\n",
      "utf8",
    );
    await writeFile(
      join(directory, "b.yaml"),
      "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \":9090\"\n" +
        "    routes: []\n" +
        "sites:\n" +
        "  docs:\n" +
        "    source:\n" +
        "      type: release\n" +
        "      slug: docs\n",
      "utf8",
    );

    const json = await runGateway([
      "--output",
      "json",
      "config",
      "diff",
      join(directory, "a.yaml"),
      join(directory, "b.yaml"),
    ]);
    expect(json.exitCode).toBe(0);
    expect(jsonOutput(json).diff).toEqual({
      added: [],
      removed: [{ section: "sites", name: "legacy" }],
      changed: [{ section: "listeners", name: "main" }],
    });

    const text = await runGateway([
      "config",
      "diff",
      join(directory, "a.yaml"),
      join(directory, "b.yaml"),
    ]);
    expect(text.exitCode).toBe(0);
    expect(text.stdout).toContain("changed listener main");
    expect(text.stdout).toContain("removed site legacy");
  });

  it("reports a whole-section change for the variables section", async () => {
    const directory = await createConfigDir();
    await writeFile(
      join(directory, "a.yaml"),
      "variables:\n" +
        "  host: 127.0.0.1\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \"${host}:8080\"\n" +
        "    routes: []\n",
      "utf8",
    );
    await writeFile(
      join(directory, "b.yaml"),
      "variables:\n" +
        "  host: 0.0.0.0\n" +
        "listeners:\n" +
        "  main:\n" +
        "    type: http\n" +
        "    address: \"${host}:8080\"\n" +
        "    routes: []\n",
      "utf8",
    );

    const result = await runGateway([
      "--output",
      "json",
      "config",
      "diff",
      join(directory, "a.yaml"),
      join(directory, "b.yaml"),
    ]);

    expect(result.exitCode).toBe(0);
    expect(jsonOutput(result).diff).toEqual({
      added: [],
      removed: [],
      changed: [{ section: "variables" }],
    });
  });
});