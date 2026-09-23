import { afterEach, describe, expect, it } from "vitest";
import { mkdir, mkdtemp, readlink, rm, stat, writeFile } from "node:fs/promises";
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

  it("rejects CLI publish for a directory source without changing registry state", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-cli-directory-"));
    directories.push(workspace);
    const registry = join(workspace, "registry");
    const source = join(workspace, "source");
    await mkdir(source);
    await writeFile(join(source, "site.yaml"), "slug: docs\n", "utf8");
    await writeFile(join(source, "index.html"), "docs\n", "utf8");
    const configPath = join(workspace, "gateway.yaml");
    const config = [
      "registry:",
      `  path: ${registry}`,
      "sites:",
      "  docs:",
      `    source: { type: directory, root: ${source} }`,
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${await freeAddress()}`,
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { site: docs }",
    ].join("\n");
    await writeFile(configPath, config, "utf8");

    const result = await runGateway(["--output", "json", "--config", configPath, "site", "publish", "docs", source]);
    const output = JSON.parse(result.stdout) as { problem?: { code?: string } };

    expect(result.exitCode).toBe(4);
    expect(output.problem?.code).toBe("site_source_immutable");
    await expect(stat(join(registry, "sites", "docs"))).rejects.toMatchObject({ code: "ENOENT" });
  });

  it("publishes a release source and returns its resulting revision", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-cli-publish-"));
    directories.push(workspace);
    const registry = join(workspace, "registry");
    const source = join(workspace, "source");
    await mkdir(source);
    await writeFile(join(source, "site.yaml"), "slug: blog\n", "utf8");
    await writeFile(join(source, "index.html"), "release body\n", "utf8");
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

    const result = await runGateway(["--output", "json", "--config", configPath, "site", "publish", "blog", source]);
    const output = JSON.parse(result.stdout) as { site?: string; revision?: string; requestId?: string; previousRevision?: string | null };

    expect(result.exitCode).toBe(0);
    expect(output.site).toBe("blog");
    expect(output.revision).toBeTruthy();
    expect(output.requestId).toMatch(/^req_[a-zA-Z0-9]+$/);
    expect(output.previousRevision).toBeNull();
    expect(await readlink(join(registry, "sites", "blog", "current"))).toContain(output.revision);
  });

  it("reports null current and previous revisions before the first publish", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-cli-empty-"));
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

    for (const pointer of ["current", "previous"] as const) {
      const result = await runGateway(["--output", "json", "--config", configPath, "site", pointer, "blog"]);
      const output = JSON.parse(result.stdout) as { revision?: string | null };
      expect(result.exitCode).toBe(0);
      expect(output.revision).toBeNull();
    }
    await expect(stat(join(registry, "sites", "blog"))).rejects.toMatchObject({ code: "ENOENT" });
  });
});
