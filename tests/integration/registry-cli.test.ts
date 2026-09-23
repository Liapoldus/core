import { afterEach, describe, expect, it } from "vitest";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { runGateway } from "../support/gateway.js";
import { freeAddress } from "../support/http.js";

const directories: string[] = [];

afterEach(async () => {
  for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true });
});

describe("registry CLI golden vectors", () => {
  it("returns no_previous_release and preserves pointers when rollback has no previous release", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-cli-"));
    directories.push(workspace);
    const registry = join(workspace, "registry");
    const configPath = join(workspace, "gateway.yaml");
    const config = [
      "registry:",
      `  path: ${registry}`,
      "sites:",
      "  blog:",
      "    source: { type: release, slug: blog }",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${await freeAddress()}`,
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { site: blog }",
    ].join("\n");
    await writeFile(configPath, config, "utf8");

    const result = await runGateway(["--output", "json", "--config", configPath, "site", "rollback", "blog"]);
    const output = JSON.parse(result.stdout) as { problem?: { code?: string } };

    expect(result.exitCode).toBe(5);
    expect(output.problem?.code).toBe("no_previous_release");
  });
});
