import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, readlink, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const token = "registry-cas-test-token";
const environment = { LIAPOLDUS_TEST_MANAGEMENT_TOKEN: token };
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const directories: string[] = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true });
});

describe("Gateway release compare-and-swap", () => {
  it("requires the expected current revision and preserves pointers on stale publish or rollback", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-cas-"));
    directories.push(workspace);
    const registry = join(workspace, "registry");
    const sources = ["first", "second", "third", "fourth"];
    for (const sourceName of sources) {
      const source = join(workspace, sourceName);
      await mkdir(source);
      await writeFile(join(source, "site.yaml"), "index: index.html\n", "utf8");
      await writeFile(join(source, "index.html"), `${sourceName} release\n`, "utf8");
    }

    const webAddress = await freeAddress();
    const managementAddress = await freeAddress();
    const config = [
      "registry:",
      `  path: ${registry}`,
      "sites:",
      "  blog:",
      "    source: { type: release, slug: blog }",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${webAddress}`,
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { site: blog }",
      "management:",
      `  listener: { address: ${managementAddress} }`,
      "  staticToken: env:LIAPOLDUS_TEST_MANAGEMENT_TOKEN",
    ].join("\n");
    const configPath = await writeGatewayConfig(config);
    const gateway = await startGateway(["--config", configPath, "serve"], environment);
    gateways.push(gateway);
    await waitReady(managementAddress);

    const headers = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
    const publish = (sourceName: string, idempotencyKey: string, expectedCurrentRevision: string | null) =>
      request(managementAddress, "/api/sites/blog/publish", {
        method: "POST",
        headers,
        body: JSON.stringify({
          source: join(workspace, sourceName),
          idempotencyKey,
          expectedCurrentRevision,
        }),
      });

    const unguarded = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify({ source: join(workspace, "first"), idempotencyKey: "5234567890abcdef" }),
    });
    const first = await publish("first", "1234567890abcdef", null);
    const firstRevision = JSON.parse(first.text) as { operationId: string; result: { revision: string } };
    const second = await publish("second", "2234567890abcdef", firstRevision.result.revision);
    const secondRevision = JSON.parse(second.text) as { operationId: string; result: { revision: string } };
    const retriedFirst = await publish("first", "1234567890abcdef", null);
    const changedPrecondition = await publish("first", "1234567890abcdef", secondRevision.result.revision);
    const contenders = await Promise.all([
      publish("third", "3234567890abcdef", secondRevision.result.revision),
      publish("fourth", "7234567890abcdef", secondRevision.result.revision),
    ]);
    const winningPublish = contenders.find((response) => response.status === 201);
    const losingPublish = contenders.find((response) => response.status === 409);
    const winningRevision = JSON.parse(winningPublish?.text ?? "{}") as { result?: { revision?: string }; operationId?: string };
    if (!winningRevision.result?.revision || !winningRevision.operationId) {
      throw new Error("winning publish did not return its operation and release revision");
    }
    const stalePublish = await publish("first", "8234567890abcdef", firstRevision.result.revision);
    const staleRollback = await request(managementAddress, "/api/sites/blog/rollback", {
      method: "POST",
      headers,
      body: JSON.stringify({ idempotencyKey: "4234567890abcdef", expectedCurrentRevision: firstRevision.result.revision }),
    });
    const successfulRollback = await request(managementAddress, "/api/sites/blog/rollback", {
      method: "POST",
      headers,
      body: JSON.stringify({ idempotencyKey: "6234567890abcdef", expectedCurrentRevision: winningRevision.result?.revision }),
    });
    const repeatedRollback = await request(managementAddress, "/api/sites/blog/rollback", {
      method: "POST",
      headers,
      body: JSON.stringify({ idempotencyKey: "6234567890abcdef", expectedCurrentRevision: winningRevision.result?.revision }),
    });
    const served = await request(webAddress, "/");
    const currentPointer = await readlink(join(registry, "sites", "blog", "current"));
    const rollbackResult = JSON.parse(successfulRollback.text) as { operationId: string; result: { revision: string } };
    const operationResponse = await request(managementAddress, `/api/operations/${secondRevision.operationId}`, { headers });
    const siteList = await request(managementAddress, "/api/sites", { headers });
    const listedSite = (JSON.parse(siteList.text) as { items: Array<{ slug: string; currentRevision: string; root: string | null }> }).items
      .find((item) => item.slug === "blog");

    expect(unguarded.status).toBe(400);
    expect(first.status).toBe(201);
    expect(second.status).toBe(201);
    expect(retriedFirst.text).toBe(first.text);
    expect(changedPrecondition.status).toBe(409);
    expect(changedPrecondition.text).toContain("idempotency_conflict");
    expect(contenders.filter((response) => response.status === 201)).toHaveLength(1);
    expect(contenders.filter((response) => response.status === 409)).toHaveLength(1);
    expect(losingPublish?.text).toContain("release_revision_conflict");
    expect(stalePublish.status).toBe(409);
    expect(JSON.parse(stalePublish.text)).toMatchObject({
      code: "release_revision_conflict",
      expectedRevision: firstRevision.result.revision,
      currentRevision: winningRevision.result.revision,
    });
    expect(staleRollback.status).toBe(409);
    expect(staleRollback.text).toContain("release_revision_conflict");
    expect(successfulRollback.status).toBe(202);
    expect(repeatedRollback.text).toBe(successfulRollback.text);
    expect(JSON.parse(operationResponse.text)).toMatchObject({ state: "succeeded", result: { revision: secondRevision.result.revision } });
    expect(listedSite).toMatchObject({ currentRevision: rollbackResult.result.revision, root: null });
    expect(siteList.text).not.toContain(workspace);
    expect(served.text).toBe("second release\n");
    expect(currentPointer).toContain(rollbackResult.result.revision);
  });
});
