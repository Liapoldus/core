import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, readdir, readlink, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const token = "registry-management-test-token";
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

describe("Management registry operations", () => {
  it("publishes an immutable release, deduplicates retries, and rejects key reuse with a new body", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-api-"));
    directories.push(workspace);
    const registry = join(workspace, "registry");
    const source = join(workspace, "source");
    await mkdir(source);
    await writeFile(join(source, "site.yaml"), "index: index.html\n", "utf8");
    await writeFile(join(source, "index.html"), "published release\n", "utf8");

    const webAddress = await freeAddress();
    const managementAddress = await freeAddress();
    const config = [
      "registry:",
      `  path: ${registry}`,
      "sites:",
      "  blog:",
      "    source: { type: release, slug: blog }",
      "  docs:",
      `    source: { type: directory, root: ${source} }`,
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
    await waitReady(webAddress);

    const headers = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
    const body = { source, idempotencyKey: "1234567890abcdef", expectedCurrentRevision: null };
    const published = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    });
    const repeated = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    });
    const firstResult = JSON.parse(published.text) as { operationId: string; result: { revision: string } };
    const repeatedResult = JSON.parse(repeated.text) as { operationId: string; result: { revision: string } };
    const conflict = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify({ ...body, source: join(workspace, "different-source") }),
    });
    const nextSources = ["second", "third"].map((name) => join(workspace, name));
    for (const [index, nextSource] of nextSources.entries()) {
      await mkdir(nextSource);
      await writeFile(join(nextSource, "site.yaml"), "index: index.html\n", "utf8");
      await writeFile(join(nextSource, "index.html"), `${index === 0 ? "second" : "third"} release\n`, "utf8");
    }
    const secondPublish = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify({ source: nextSources[0], idempotencyKey: "2234567890abcdef", expectedCurrentRevision: firstResult.result.revision }),
    });
    const secondRevision = JSON.parse(secondPublish.text) as { operationId: string; result: { revision: string } };
    const thirdPublish = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify({ source: nextSources[1], idempotencyKey: "3234567890abcdef", expectedCurrentRevision: secondRevision.result.revision }),
    });
    const extraProperty = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify({ ...body, unexpected: true }),
    });
    const trailingValue = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: `${JSON.stringify(body)} {}`,
    });
    const served = await request(webAddress, "/");
    const auditResponse = await request(managementAddress, "/api/audit", { headers });
    const audit = JSON.parse(auditResponse.text) as { items: Array<{ action: string }> };
    const releasesRoot = join(registry, "sites", "blog");
    const releases = await readdir(join(releasesRoot, "releases"));
    const thirdRevision = JSON.parse(thirdPublish.text) as { operationId: string; result: { revision: string } };

    expect(published.status).toBe(201);
    expect(repeated.status).toBe(201);
    expect(repeatedResult.operationId).toBe(firstResult.operationId);
    expect(repeated.text).toBe(published.text);
    expect(repeated.headers.get("x-request-id")).toBe(published.headers.get("x-request-id"));
    expect(conflict.status).toBe(409);
    expect(extraProperty.status).toBe(400);
    expect(trailingValue.status).toBe(400);
    expect(secondPublish.status).toBe(201);
    expect(thirdPublish.status).toBe(201);
    expect(served.status).toBe(200);
    expect(served.text).toBe("third release\n");
    expect(await readlink(join(releasesRoot, "current"))).toContain(thirdRevision.result.revision);
    expect(await readlink(join(releasesRoot, "previous"))).toContain(secondRevision.result.revision);
    expect(releases).toContain(thirdRevision.result.revision);
    expect(releases).toContain(secondRevision.result.revision);
    expect(releases).not.toContain(firstResult.result.revision);
    expect(audit.items.filter((item) => item.action === "site_published")).toHaveLength(3);
    expect(auditResponse.text).not.toContain(source);
  });

  it("rejects publish for a configured directory source", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-readonly-"));
    directories.push(workspace);
    await writeFile(join(workspace, "site.yaml"), "index: index.html\n", "utf8");
    await writeFile(join(workspace, "index.html"), "directory source\n", "utf8");
    const managementAddress = await freeAddress();
    const config = [
      "sites:",
      "  docs:",
      `    source: { type: directory, root: ${workspace} }`,
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${await freeAddress()}`,
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { site: docs }",
      "management:",
      `  listener: { address: ${managementAddress} }`,
      "  staticToken: env:LIAPOLDUS_TEST_MANAGEMENT_TOKEN",
    ].join("\n");
    const configPath = await writeGatewayConfig(config);
    const gateway = await startGateway(["--config", configPath, "serve"], environment);
    gateways.push(gateway);
    await waitReady(managementAddress);

    const response = await request(managementAddress, "/api/sites/docs/publish", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify({ source: workspace, idempotencyKey: "1234567890abcdef", expectedCurrentRevision: null }),
    });

    expect(response.status).toBe(409);
    expect(response.text).toContain("site_source_immutable");
  });

  it("reports a missing rollback target without exposing registry paths", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-registry-no-rollback-"));
    directories.push(workspace);
    const managementAddress = await freeAddress();
    const config = [
      "registry:",
      `  path: ${join(workspace, "registry")}`,
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
      "management:",
      `  listener: { address: ${managementAddress} }`,
      "  staticToken: env:LIAPOLDUS_TEST_MANAGEMENT_TOKEN",
    ].join("\n");
    const configPath = await writeGatewayConfig(config);
    const gateway = await startGateway(["--config", configPath, "serve"], environment);
    gateways.push(gateway);
    await waitReady(managementAddress);

    const response = await request(managementAddress, "/api/sites/blog/rollback", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify({ idempotencyKey: "1234567890abcdef", expectedCurrentRevision: null }),
    });

    expect(response.status).toBe(404);
    expect(response.text).toContain("no_previous_release");
    expect(response.text).not.toContain(workspace);
  });
});
