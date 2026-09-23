import { readFileSync } from "node:fs";
import type { ChildProcess } from "node:child_process";
import { afterEach, describe, expect, it } from "vitest";
import { createServer, type Server } from "node:http";
import { startGateway, startGatewayWithOutput } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";
import { startUpstream } from "../support/upstream.js";
import { resolve } from "node:path";

const gatewayToken = "telemetry-export-test-token";
const environment = { LIAPOLDUS_TEST_TELEMETRY_TOKEN: gatewayToken };
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const upstreams: Array<{ stop(): Promise<void> }> = [];
const collectors: Server[] = [];

const telemetryVectors = (() => {
  const source = readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8");
  const contract = JSON.parse(source) as {
    vectors: Array<{
      id: string;
      input: { otlp?: string };
      expected: { trafficStatus: number; exporterFailureLogged: boolean };
    }>;
  };
  return ["telemetry-export-failure", "metrics-exporter-isolation"].map((id) => {
    const vector = contract.vectors.find((candidate) => candidate.id === id);
    if (!vector) throw new Error(`${id} golden vector is missing`);
    return vector;
  });
})();

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const upstream of upstreams.splice(0)) await upstream.stop();
  for (const collector of collectors.splice(0)) {
    await new Promise<void>((resolveClose, reject) => collector.close((error) => error ? reject(error) : resolveClose()));
  }
});

async function startTelemetryGateway(endpoint: string, captureOutput = false, applicationSink?: string): Promise<{
  webAddress: string;
  managementAddress: string;
  output?: { stdout: string; stderr: string };
}> {
  const upstream = await startUpstream();
  upstreams.push(upstream);
  const webAddress = await freeAddress();
  const managementAddress = await freeAddress();
  const configLines = [
    "upstreams:", "  api:", "    targets:", `      - address: ${upstream.address}`,
    "listeners:", "  web:", "    type: http", `    address: ${webAddress}`,
    "    routes:", "      - when: { path: { prefix: / } }", "        then: { proxy: { upstream: api } }",
    "management:", `  listener: { address: ${managementAddress} }`,
    "  staticToken: env:LIAPOLDUS_TEST_TELEMETRY_TOKEN",
    "metrics:", "  prometheus: true", `  otlp: { endpoint: ${endpoint}, interval: 100ms }`,
  ];
  if (applicationSink !== undefined) configLines.push("logging:", `  application: [${applicationSink}]`);
  const config = await writeGatewayConfig(configLines.join("\n"));
  const gateway = captureOutput
    ? await startGatewayWithOutput(["--config", config, "serve"], environment)
    : await startGateway(["--config", config, "serve"], environment);
  gateways.push(gateway);
  await waitReady(webAddress);
  await waitReady(managementAddress);
  return {
    webAddress,
    managementAddress,
    ...(captureOutput ? { output: gateway } : {}),
  };
}

describe("telemetry exporter isolation", () => {
  it("keeps HTTP traffic healthy and records unavailable OTLP metrics exports", async () => {
    const unavailableOTLPAddress = await freeAddress();
    const { webAddress, managementAddress, output } = await startTelemetryGateway(`http://${unavailableOTLPAddress}/v1/metrics`, true);

    const response = await request(webAddress, "/health");
    expect(response.status).toBe(telemetryVectors[0].expected.trafficStatus);
    expect(response.status).toBe(telemetryVectors[1].expected.trafficStatus);

    let metrics = "";
    const deadline = Date.now() + 3000;
    while (Date.now() < deadline) {
      const scrape = await request(managementAddress, "/metrics", {
        headers: { Authorization: `Bearer ${gatewayToken}` },
      });
      metrics = scrape.text;
      if (metrics.includes("liapoldus_otel_export_failures_total")) break;
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 50));
    }

    expect(metrics).toContain("liapoldus_otel_export_failures_total");
    expect(metrics).toMatch(/liapoldus_otel_export_failures_total\{exporter="otlp"\} [1-9]/);
    expect(telemetryVectors.every(({ expected }) => expected.exporterFailureLogged)).toBe(true);
    expect(output?.stderr).toContain("OpenTelemetry metrics export failed");
    expect(output?.stderr).not.toContain(unavailableOTLPAddress);
  });

  it("routes exporter warnings to configured application sinks without logging the endpoint", async () => {
    const unavailableOTLPAddress = await freeAddress();
    const { webAddress, managementAddress, output } = await startTelemetryGateway(`http://${unavailableOTLPAddress}/v1/metrics`, true, "stdout");

    const response = await request(webAddress, "/configured-logging");
    expect(response.status).toBe(200);

    let metrics = "";
    const deadline = Date.now() + 3000;
    while (Date.now() < deadline) {
      const scrape = await request(managementAddress, "/metrics", {
        headers: { Authorization: `Bearer ${gatewayToken}` },
      });
      metrics = scrape.text;
      if (metrics.includes("liapoldus_otel_export_failures_total")) break;
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 50));
    }

    expect(metrics).toMatch(/liapoldus_otel_export_failures_total\{exporter="otlp"\} [1-9]/);
    const outputDeadline = Date.now() + 1000;
    while (!output?.stdout.includes("OpenTelemetry metrics export failed") && Date.now() < outputDeadline) {
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 25));
    }
    expect(output?.stdout).toContain("OpenTelemetry metrics export failed");
    expect(output?.stdout).not.toContain(unavailableOTLPAddress);
    expect(output?.stderr).not.toContain("OpenTelemetry metrics export failed");
    expect(output?.stderr).not.toContain(unavailableOTLPAddress);
  }, 20_000);

  it("sends Prometheus runtime measurements as OTLP/HTTP protobuf metrics", async () => {
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
    await new Promise<void>((resolveListen, reject) => {
      collector.once("error", reject);
      collector.listen(0, "127.0.0.1", resolveListen);
    });
    const address = collector.address();
    if (address === null || typeof address === "string") throw new Error("OTLP receiver did not bind TCP");
    const { webAddress } = await startTelemetryGateway(`http://127.0.0.1:${address.port}/v1/metrics`);

    const response = await request(webAddress, "/telemetry");
    expect(response.status).toBe(telemetryVectors[0].expected.trafficStatus);
    const deadline = Date.now() + 3000;
    while (bodies.length === 0 && Date.now() < deadline) await new Promise((resolveDelay) => setTimeout(resolveDelay, 50));

    expect(requestPath).toBe("/v1/metrics");
    expect(contentType).toMatch(/application\/x-protobuf/);
    expect(bodies[0]?.byteLength).toBeGreaterThan(0);
  });
});
