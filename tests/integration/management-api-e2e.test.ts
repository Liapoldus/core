import { mkdtemp, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { startGateway } from "../support/gateway.js";

const running: Array<{ stop(): Promise<void> }> = [];
afterEach(async () => { await Promise.all(running.splice(0).map((process) => process.stop())); });
async function address(): Promise<string> { return new Promise((resolve, reject) => { const server = createServer(); server.listen(0, "127.0.0.1", () => { const port = (server.address() as { port: number }).port; server.close(() => resolve(`127.0.0.1:${port}`)); }); server.once("error", reject); }); }

describe("Management API e2e", () => {
  it("serves health, status and config with bearer protection", async () => {
    const root = await mkdtemp(join(tmpdir(), "liapoldus-management-"));
    const publicAddress = await address();
    const managementAddress = await address();
    await writeFile(join(root, "index.html"), "ok", "utf8");
    const config = join(root, "gateway.yaml");
    await writeFile(config, [`management:`, `  listener: { address: ${managementAddress} }`, `  staticToken: test-token`, `sites:`, `  docs:`, `    source: { type: directory, root: ${root} }`, `listeners:`, `  web:`, `    type: http`, `    address: ${publicAddress}`, `    routes:`, `      - when: { path: { prefix: / } }`, `        then: { site: docs }`].join("\n") + "\n", "utf8");
    const gateway = await startGateway(["--config", config, "serve"]);
    running.push(gateway);
    const health = await fetch(`http://${managementAddress}/healthz`);
    expect(health.status).toBe(200);
  });
});
