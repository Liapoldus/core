import type { ChildProcess } from "node:child_process";
import { afterEach, describe, expect, it } from "vitest";
import { startGatewayWithOutput } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";
import { startUpstream } from "../support/upstream.js";

const gatewayToken = "access-logging-test-token";
const environment = { LIAPOLDUS_TEST_ACCESS_TOKEN: gatewayToken };
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const upstreams: Array<{ stop(): Promise<void> }> = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const upstream of upstreams.splice(0)) await upstream.stop();
});

describe("configured access logging", () => {
  it("writes structured request records and redacts query and header secrets", async () => {
    const upstream = await startUpstream();
    upstreams.push(upstream);
    const webAddress = await freeAddress();
    const managementAddress = await freeAddress();
    const config = await writeGatewayConfig([
      "upstreams:", "  api:", "    targets:", `      - address: ${upstream.address}`,
      "listeners:", "  web:", "    type: http", `    address: ${webAddress}`,
      "    routes:", "      - when: { path: { prefix: / } }", "        then: { proxy: { upstream: api } }",
      "management:", `  listener: { address: ${managementAddress} }`,
      "  staticToken: env:LIAPOLDUS_TEST_ACCESS_TOKEN",
      "logging:", "  format: json", "  access: [stderr]",
    ].join("\n"));
    const gateway = await startGatewayWithOutput(["--config", config, "serve"], environment);
    gateways.push(gateway);
    await waitReady(webAddress);

    const requestId = "access-log-request-42";
    const response = await request(webAddress, "/private?token=query-secret", {
      method: "POST",
      headers: {
        "x-request-id": requestId,
        authorization: "Bearer header-secret",
        cookie: "session=cookie-secret",
      },
      body: "request-body",
    });
    expect(response.status).toBe(200);
    await gateway.stop();

    const records = gateway.stderr
      .split("\n")
      .filter((line) => line.length > 0)
      .map((line) => JSON.parse(line) as Record<string, unknown>);
    const record = records.find((line) => line.requestId === requestId);

    expect(record).toBeDefined();
    expect(record).toMatchObject({
      requestId,
      listener: webAddress,
      route: "/private",
      method: "POST",
      path: "/private",
      status: 200,
    });
    expect(Number(record?.bytes)).toBeGreaterThan(0);
    expect(typeof record?.host).toBe("string");
    expect(Date.parse(String(record?.timestamp))).not.toBeNaN();
    expect(Number(record?.duration)).toBeGreaterThanOrEqual(0);
    expect(gateway.stderr).not.toContain("query-secret");
    expect(gateway.stderr).not.toContain("header-secret");
    expect(gateway.stderr).not.toContain("cookie-secret");
  });

  it("generates and returns a request ID when the caller did not provide one", async () => {
    const upstream = await startUpstream();
    upstreams.push(upstream);
    const webAddress = await freeAddress();
    const config = await writeGatewayConfig([
      "upstreams:", "  api:", "    targets:", `      - address: ${upstream.address}`,
      "listeners:", "  web:", "    type: http", `    address: ${webAddress}`,
      "    routes:", "      - when: { path: { prefix: / } }", "        then: { proxy: { upstream: api } }",
      "logging:", "  format: json", "  access: [stderr]",
    ].join("\n"));
    const gateway = await startGatewayWithOutput(["--config", config, "serve"]);
    gateways.push(gateway);
    await waitReady(webAddress);

    const response = await request(webAddress, "/generated");
    expect(response.status).toBe(200);
    await gateway.stop();

    const record = gateway.stderr
      .split("\n")
      .filter((line) => line.length > 0)
      .map((line) => JSON.parse(line) as Record<string, unknown>)
      .find((line) => line.path === "/generated");
    expect(typeof record?.requestId).toBe("string");
    expect(String(record?.requestId).length).toBeGreaterThan(0);
    expect(response.headers.get("x-request-id")).toBe(record?.requestId);
  });
});
