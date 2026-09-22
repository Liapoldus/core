import { copyFile, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";
import { startUpstream } from "../support/upstream.js";

const token = "reload-test-token";
const environment = { LIAPOLDUS_TEST_MANAGEMENT_TOKEN: token };
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const servers: Array<{ stop(): Promise<void> }> = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const server of servers.splice(0)) await server.stop();
});

describe("atomic configuration and MMDB reload", () => {
  it("keeps the active revision and traffic when a replacement MMDB cannot be verified", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const webAddress = await freeAddress();
    const managementAddress = await freeAddress();
    const fixtureDirectory = await mkdtemp(join(tmpdir(), "liapoldus-reload-mmdb-"));
    const validDatabase = join(fixtureDirectory, "city.mmdb");
    const brokenDatabase = join(fixtureDirectory, "broken.mmdb");
    await copyFile("fixtures/GeoIP2-City-Test.mmdb", validDatabase);
    await writeFile(brokenDatabase, "not an MMDB", "utf8");

    const initialConfig = [
      `dataProviders:`, `  geo: { type: mmdb, path: ${validDatabase}, onError: deny }`,
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  geo:`, `    rules:`,
      `      - when: { geo: { provider: geo, country: { exact: US } }, path: { prefix: /api } }`,
      `        onError: allow`, `        then: { deny: { status: 451 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${webAddress}`,
      `    routes:`, `      - when: { path: { prefix: /api } }`,
      `        then: { proxy: api, waf: geo }`,
      `management:`, `  listener: { address: ${managementAddress} }`,
      `  staticToken: env:LIAPOLDUS_TEST_MANAGEMENT_TOKEN`,
    ].join("\n");
    const configPath = await writeGatewayConfig(initialConfig);
    const gateway = await startGateway(["--config", configPath, "serve"], environment);
    gateways.push(gateway);
    await waitReady(webAddress);
    await waitReady(managementAddress);

    const auth = { Authorization: `Bearer ${token}` };
    const before = await request(managementAddress, "/api/status", { headers: auth });
    const beforeStatus = JSON.parse(before.text) as { revision: string; digest: string };
    const firstTraffic = await request(webAddress, "/api/private");
    expect(firstTraffic.status).toBe(200);

    await writeFile(configPath, initialConfig.replace(validDatabase, brokenDatabase), "utf8");
    const reload = await request(managementAddress, "/api/reload", {
      method: "POST",
      headers: { ...auth, "If-Match": beforeStatus.revision },
    });
    const after = await request(managementAddress, "/api/status", { headers: auth });
    const afterStatus = JSON.parse(after.text) as { revision: string; digest: string };
    const followingTraffic = await request(webAddress, "/api/private");

    expect(reload.status).toBe(422);
    expect(afterStatus.revision).toBe(beforeStatus.revision);
    expect(afterStatus.digest).toBe(beforeStatus.digest);
    expect(followingTraffic.status).toBe(200);
    expect(upstream.hits().paths).toEqual(["/api/private", "/api/private"]);
  });
});
