import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, readdir, readlink, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const gatewayToken = "release-symlink-test-token";
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const workspaces: string[] = [];

const symlinkVector = (() => {
  const source = readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8");
  const contract = JSON.parse(source) as {
    vectors: Array<{
      id: string;
      input: { releaseEntry: string };
      expected: { status: number; code: string; currentChanged: boolean };
    }>;
  };
  const vector = contract.vectors.find(({ id }) => id === "release-symlink");
  if (!vector) throw new Error("release-symlink golden vector is missing");
  return vector;
})();

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const workspace of workspaces.splice(0)) await rm(workspace, { recursive: true, force: true });
});

describe("release symlink validation", () => {
  it("rejects symlink entries without changing the current release", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-release-symlink-"));
    workspaces.push(workspace);
    const registry = join(workspace, "registry");
    const validSource = join(workspace, "valid-source");
    const invalidSource = join(workspace, "invalid-source");
    const outside = join(workspace, "outside-secret-file");
    await mkdir(validSource);
    await mkdir(invalidSource);
    await writeFile(outside, "must-not-be-copied", "utf8");
    for (const source of [validSource, invalidSource]) {
      await writeFile(join(source, "site.yaml"), "index: index.html\n", "utf8");
      await writeFile(join(source, "index.html"), "release\n", "utf8");
    }
    const escapeName = symlinkVector.input.releaseEntry.split(" -> ")[0];
    if (!escapeName) throw new Error("release-symlink vector has no link name");
    await symlink(outside, join(invalidSource, escapeName));

    const managementAddress = await freeAddress();
    const webAddress = await freeAddress();
    const config = await writeGatewayConfig([
      "registry:", `  path: ${registry}`,
      "sites:", "  blog:", "    source: { type: release, slug: blog }",
      "listeners:", "  web:", "    type: http", `    address: ${webAddress}`, "    routes: []",
      "management:", `  listener: { address: ${managementAddress} }`,
      "  staticToken: env:LIAPOLDUS_TEST_RELEASE_TOKEN",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve"], {
      LIAPOLDUS_TEST_RELEASE_TOKEN: gatewayToken,
    });
    gateways.push(gateway);
    await waitReady(managementAddress);

    const headers = { Authorization: `Bearer ${gatewayToken}`, "Content-Type": "application/json" };
    const initial = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify({ source: validSource, idempotencyKey: "initial-release-0001", expectedCurrentRevision: null }),
    });
    const initialBody = JSON.parse(initial.text) as { result: { revision: string } };
    const currentLink = join(registry, "sites", "blog", "current");
    const linkBefore = await readlink(currentLink);
    const rejected = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify({ source: invalidSource, idempotencyKey: "invalid-release-0001", expectedCurrentRevision: initialBody.result.revision }),
    });
    const rejectedBody = JSON.parse(rejected.text) as { code?: string };

    expect(initial.status).toBe(201);
    expect(rejected.status).toBe(symlinkVector.expected.status);
    expect(rejectedBody.code).toBe(symlinkVector.expected.code);
    expect((await readlink(currentLink)) !== linkBefore).toBe(symlinkVector.expected.currentChanged);
    expect(await readdir(join(registry, "sites", "blog", "releases"))).toEqual([initialBody.result.revision]);
    expect(rejected.text).not.toContain(outside);
  });
});
