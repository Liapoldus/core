import { request as httpRequest } from "node:http";
import type { IncomingHttpHeaders } from "node:http";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";
import { startUpstream } from "../support/upstream.js";

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const upstreams: Array<{ stop(): Promise<void> }> = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const upstream of upstreams.splice(0)) await upstream.stop();
});

function sendChunked(address: string, path: string, chunks: string[]): Promise<{ status: number; headers: IncomingHttpHeaders; body: string }> {
  return new Promise((resolve, reject) => {
    const [host, port] = address.split(":");
    const outgoing = httpRequest({ host, port: Number(port), path, method: "POST", headers: { "transfer-encoding": "chunked" } }, (incoming) => {
      const responseChunks: Buffer[] = [];
      incoming.on("data", (chunk: Buffer) => responseChunks.push(chunk));
      incoming.on("end", () => resolve({
        status: incoming.statusCode ?? 0,
        headers: incoming.headers,
        body: Buffer.concat(responseChunks).toString("utf8"),
      }));
    });
    outgoing.once("error", reject);
    for (const chunk of chunks) outgoing.write(chunk);
    outgoing.end();
  });
}

describe("WAF requestSize", () => {
  it("matches actual HTTP body bytes and preserves an allowed body for the upstream", async () => {
    const upstream = await startUpstream();
    upstreams.push(upstream);
    const listener = await freeAddress();
    const config = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  body-size:`, `    rules:`,
      `      - when: { requestSize: { gt: 3 } }`,
      `        then: { deny: { status: 451 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${listener}`,
      `    limits: { bodyBytes: 16 }`,
      `    routes:`, `      - when: { path: { prefix: / } }`,
      `        then: { proxy: { upstream: api }, waf: body-size }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(listener);
    const baselineBodies = upstream.hits().bodies.length;

    const allowed = await request(listener, "/allowed", { method: "POST", body: "abc" });
    const denied = await request(listener, "/denied", { method: "POST", body: "abcd" });

    expect(allowed.status).toBe(200);
    expect(denied.status).toBe(451);
    expect(upstream.hits().bodies.slice(baselineBodies)).toEqual(["abc"]);
  });

  it("measures chunked bodies by received bytes and forwards allowed chunks unchanged", async () => {
    const upstream = await startUpstream();
    upstreams.push(upstream);
    const listener = await freeAddress();
    const config = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  body-size:`, `    rules:`,
      `      - when: { requestSize: { gte: 4 } }`,
      `        then: { deny: { status: 451 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${listener}`,
      `    limits: { bodyBytes: 16 }`,
      `    routes:`, `      - when: { path: { prefix: / } }`,
      `        then: { proxy: { upstream: api }, waf: body-size }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(listener);
    const baselineBodies = upstream.hits().bodies.length;

    const allowed = await sendChunked(listener, "/allowed", ["a", "bc"]);
    const denied = await sendChunked(listener, "/denied", ["ab", "cd"]);

    expect(allowed.status).toBe(200);
    expect(denied.status).toBe(451);
    expect(upstream.hits().bodies.slice(baselineBodies)).toEqual(["abc"]);
  });

  it("accepts binary size units at exact comparison boundaries", async () => {
    const upstream = await startUpstream();
    upstreams.push(upstream);
    const listener = await freeAddress();
    const config = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  body-size:`, `    rules:`,
      `      - when: { requestSize: { gte: 1KiB } }`,
      `        then: { deny: { status: 451 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${listener}`,
      `    limits: { bodyBytes: 2KiB }`,
      `    routes:`, `      - when: { path: { prefix: / } }`,
      `        then: { proxy: { upstream: api }, waf: body-size }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(listener);
    const baseline = upstream.hits().requests;

    const below = await request(listener, "/below", { method: "POST", body: "x".repeat(1023) });
    const atBoundary = await request(listener, "/boundary", { method: "POST", body: "x".repeat(1024) });

    expect(below.status).toBe(200);
    expect(atBoundary.status).toBe(451);
    expect(upstream.hits().requests - baseline).toBe(1);
    expect(upstream.hits().bodies.at(-1)).toBe("x".repeat(1023));
  });

  it("rejects a body over listener bodyBytes before dispatching it", async () => {
    const upstream = await startUpstream();
    upstreams.push(upstream);
    const listener = await freeAddress();
    const config = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${listener}`,
      `    limits: { bodyBytes: 3 }`,
      `    routes:`, `      - when: { path: { prefix: / } }`,
      `        then: { proxy: { upstream: api } }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(listener);
    const baselineRequests = upstream.hits().requests;

    const response = await request(listener, "/too-large", { method: "POST", body: "four" });

    expect(response.status).toBe(413);
    expect(response.text).toContain("body_too_large");
    expect(upstream.hits().requests).toBe(baselineRequests);
  });
});
