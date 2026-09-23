import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const token = "rollback-audit-test-token";
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

describe("release rollback audit", () => {
  it("records a successful rollback without exposing source paths or credentials", async () => {
    const workspace = await mkdtemp(join(tmpdir(), "liapoldus-rollback-audit-"));
    directories.push(workspace);
    const registry = join(workspace, "registry");
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

    const headers = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
    const sources = ["first", "second"].map((name) => join(workspace, name));
    for (const [index, source] of sources.entries()) {
      await mkdir(source);
      await writeFile(join(source, "site.yaml"), "index: index.html\n", "utf8");
      await writeFile(join(source, "index.html"), `${index === 0 ? "first" : "second"} release\n`, "utf8");
    }
    const firstPublish = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify({ source: sources[0], idempotencyKey: "1234567890abcdef", expectedCurrentRevision: null }),
    });
    const first = JSON.parse(firstPublish.text) as { result: { revision: string } };
    const secondPublish = await request(managementAddress, "/api/sites/blog/publish", {
      method: "POST",
      headers,
      body: JSON.stringify({ source: sources[1], idempotencyKey: "2234567890abcdef", expectedCurrentRevision: first.result.revision }),
    });
    const second = JSON.parse(secondPublish.text) as { result: { revision: string } };
    const rollback = await request(managementAddress, "/api/sites/blog/rollback", {
      method: "POST",
      headers,
      body: JSON.stringify({ idempotencyKey: "3234567890abcdef", expectedCurrentRevision: second.result.revision }),
    });
    const auditResponse = await request(managementAddress, "/api/audit", { headers });
    const audit = JSON.parse(auditResponse.text) as {
      items: Array<{ actor: string; action: string; resource: string; result: string; requestId: string }>;
    };

    expect(firstPublish.status).toBe(201);
    expect(secondPublish.status).toBe(201);
    expect(rollback.status).toBe(202);
    expect(auditResponse.status).toBe(200);
    expect(audit.items).toContainEqual(expect.objectContaining({
      actor: "static-token",
      action: "site_rolled_back",
      resource: "blog",
      result: "succeeded",
      requestId: rollback.headers.get("x-request-id"),
    }));
    expect(auditResponse.text).not.toContain(token);
    expect(auditResponse.text).not.toContain(workspace);
  }, 20_000);
});
