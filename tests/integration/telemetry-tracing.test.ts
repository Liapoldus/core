import { createConnection, type Socket } from "node:net";
import { createServer, type Server } from "node:http";
import type { ChildProcess } from "node:child_process";
import { afterEach, describe, expect, it } from "vitest";
import { startGateway } from "../support/gateway.js";
import { freeAddress, portOf, request, writeGatewayConfig } from "../support/http.js";
import { startUpstream } from "../support/upstream.js";

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const upstreams: Array<{ stop(): Promise<void> }> = [];
const collectors: Server[] = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const upstream of upstreams.splice(0)) await upstream.stop();
  for (const collector of collectors.splice(0)) {
    await new Promise<void>((resolve, reject) => collector.close((error) => error ? reject(error) : resolve()));
  }
});

async function waitListening(address: string): Promise<void> {
  const deadline = Date.now() + 6000;
  while (Date.now() < deadline) {
    const socket: Socket = createConnection({ host: "127.0.0.1", port: Number(portOf(address)) });
    const connected = await new Promise<boolean>((resolve) => {
      socket.once("connect", () => resolve(true));
      socket.once("error", () => resolve(false));
    });
    socket.destroy();
    if (connected) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("gateway listener did not become ready");
}

describe("OTLP tracing and W3C parent-based sampling", () => {
  it("exports spans for sampled parent context and drops unsampled parent context", async () => {
    const bodies: Buffer[] = [];
    let requestPath = "";
    let contentType = "";
    const collector = createServer((incoming, response) => {
      requestPath = incoming.url ?? "";
      contentType = String(incoming.headers["content-type"] ?? "");
      const chunks: Buffer[] = [];
      incoming.on("data", (chunk: Buffer) => chunks.push(chunk));
      incoming.on("end", () => {
        bodies.push(Buffer.concat(chunks));
        response.writeHead(200);
        response.end();
      });
    });
    collectors.push(collector);
    await new Promise<void>((resolve, reject) => {
      collector.once("error", reject);
      collector.listen(0, "127.0.0.1", resolve);
    });
    const collectorAddress = collector.address();
    if (collectorAddress === null || typeof collectorAddress === "string") {
      throw new Error("OTLP collector did not bind TCP");
    }

    const upstream = await startUpstream();
    upstreams.push(upstream);
    const webAddress = await freeAddress();
    const config = await writeGatewayConfig([
      "upstreams:", "  api:", "    targets:", `      - address: ${upstream.address}`,
      "listeners:", "  web:", "    type: http", `    address: ${webAddress}`,
      "    routes:", "      - when: { path: { prefix: / } }", "        then: { proxy: { upstream: api } }",
      "tracing:", "  sampling: parent-based", `  otlp: { endpoint: http://127.0.0.1:${collectorAddress.port}/v1/traces }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve"]);
    gateways.push(gateway);
    await waitListening(webAddress);

    const sampled = await request(webAddress, "/sampled", {
      headers: { traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" },
    });
    expect(sampled.status).toBe(200);
    const exportDeadline = Date.now() + 10000;
    while (bodies.length === 0 && Date.now() < exportDeadline) {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }

    expect(bodies.length).toBeGreaterThan(0);
    expect(requestPath).toBe("/v1/traces");
    expect(contentType).toMatch(/application\/x-protobuf/);
    expect(bodies.some((body) => body.byteLength > 0)).toBe(true);
    const exportsAfterSampledParent = bodies.length;

    const unsampled = await request(webAddress, "/unsampled", {
      headers: { traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00" },
    });
    expect(unsampled.status).toBe(200);
    await new Promise((resolve) => setTimeout(resolve, 6000));
    expect(bodies).toHaveLength(exportsAfterSampledParent);
  });
});
