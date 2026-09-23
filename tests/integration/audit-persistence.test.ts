import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const token = "audit-persistence-test-token";
const environment = { LIAPOLDUS_TEST_MANAGEMENT_TOKEN: token };
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const registryDirectories: string[] = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const directory of registryDirectories.splice(0)) await rm(directory, { recursive: true, force: true });
});

describe("persistent audit JSONL", () => {
  it("records successful config reloads and serves them after Gateway restart", async () => {
    const registry = await mkdtemp(join(tmpdir(), "liapoldus-audit-"));
    registryDirectories.push(registry);
    const managementAddress = await freeAddress();
    const listenerAddress = await freeAddress();
    const config = [
      "registry:",
      `  path: ${registry}`,
      "upstreams:",
      "  api:",
      "    targets:",
      "      - address: 127.0.0.1:9",
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${listenerAddress}`,
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { proxy: { upstream: api } }",
      "management:",
      `  listener: { address: ${managementAddress} }`,
      "  staticToken: env:LIAPOLDUS_TEST_MANAGEMENT_TOKEN",
    ].join("\n");
    const configPath = await writeGatewayConfig(config);
    const start = async () => {
      const gateway = await startGateway(["--config", configPath, "serve"], environment);
      gateways.push(gateway);
      await waitReady(managementAddress);
      return gateway;
    };

    const firstGateway = await start();
    const authorization = { Authorization: `Bearer ${token}` };
    const before = await request(managementAddress, "/api/status", { headers: authorization });
    const { revision } = JSON.parse(before.text) as { revision: string };
    const update = await request(managementAddress, "/api/config", {
      method: "PUT",
      headers: { ...authorization, "If-Match": revision, "Content-Type": "application/json" },
      body: JSON.stringify({ yaml: `${config}\n# audited configuration update\n` }),
    });
    const afterUpdate = await request(managementAddress, "/api/status", { headers: authorization });
    const updatedRevision = JSON.parse(afterUpdate.text) as { revision: string };
    const reload = await request(managementAddress, "/api/reload", {
      method: "POST",
      headers: { ...authorization, "If-Match": updatedRevision.revision },
    });
    const auditResponse = await request(managementAddress, "/api/audit", { headers: authorization });
    const audit = JSON.parse(auditResponse.text) as {
      items: Array<{ actor: string; action: string; resource: string; result: string; requestId: string }>;
    };

    expect(update.status).toBe(202);
    expect(reload.status).toBe(202);
    expect(auditResponse.status).toBe(200);
    expect(audit.items).toContainEqual(expect.objectContaining({
      actor: "static-token",
      action: "config.update",
      resource: "gateway",
      result: "succeeded",
      requestId: update.headers.get("x-request-id"),
    }));
    expect(audit.items).toContainEqual(expect.objectContaining({
      actor: "static-token",
      action: "config.reload",
      resource: "gateway",
      result: "succeeded",
      requestId: reload.headers.get("x-request-id"),
    }));
    expect(auditResponse.text).not.toContain(token);

    firstGateway.process.kill("SIGTERM");
    await firstGateway.stop();
    gateways.splice(gateways.indexOf(firstGateway), 1);

    const restartedGateway = await start();
    const persisted = await request(managementAddress, "/api/audit", { headers: authorization });
    const persistedAudit = JSON.parse(persisted.text) as { items: unknown[] };

    expect(restartedGateway.process.killed).toBe(false);
    expect(persisted.status).toBe(200);
    expect(persistedAudit.items).toContainEqual(expect.objectContaining({
      action: "config.reload",
      result: "succeeded",
    }));
  });
});
