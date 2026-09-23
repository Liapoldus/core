import { createServer, type Server } from "node:http";
import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import tls from "node:tls";
import type { ChildProcess } from "node:child_process";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import { startGateway } from "../support/gateway.js";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const upstreams: Server[] = [];
const directories: string[] = [];

afterEach(async () => {
  await Promise.all(gateways.splice(0).map((gateway) => gateway.stop()));
  await Promise.all(upstreams.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))));
  await Promise.all(directories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

async function startHTTP3(options: { limits: string; trackConcurrency?: boolean }): Promise<{
  address: string;
  certificate: string;
  directory: string;
  maximumConcurrentRequests(): number;
}> {
  const directory = await mkdtemp(join(tmpdir(), "liapoldus-http3-limits-"));
  directories.push(directory);
  const certificate = join(directory, "server.crt");
  const key = join(directory, "server.key");
  await execFileAsync("openssl", [
    "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
    "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost", "-keyout", key, "-out", certificate,
  ]);

  let activeRequests = 0;
  let maximum = 0;
  const upstream = createServer((_request, response) => {
    activeRequests++;
    maximum = Math.max(maximum, activeRequests);
    setTimeout(() => {
      response.end("ok");
      activeRequests--;
    }, 450);
  });
  upstreams.push(upstream);
  const upstreamAddress = await new Promise<string>((resolve, reject) => {
    upstream.once("error", reject);
    upstream.listen(0, "127.0.0.1", () => {
      const bound = upstream.address();
      if (bound === null || typeof bound === "string") reject(new Error("upstream did not bind a TCP address"));
      else resolve(`127.0.0.1:${bound.port}`);
    });
  });

  const address = await freeAddress();
  const gatewayConfig = join(directory, "gateway.yaml");
  await writeFile(gatewayConfig, [
    "tlsProfiles:",
    "  public:",
    `    certificates: [{ cert: ${JSON.stringify(certificate)}, key: ${JSON.stringify(key)} }]`,
    "    protocols: [http/1.1, h2, h3]",
    "upstreams:",
    "  app:",
    "    targets:",
    `      - address: ${upstreamAddress}`,
    "listeners:",
    "  public:",
    "    type: http",
    `    address: ${address}`,
    "    tls: public",
    `    limits: { quic: { ${options.limits} } }`,
    "    routes:",
    "      - when: { path: { prefix: / } }",
    "        then: { proxy: { upstream: app } }",
  ].join("\n"), "utf8");

  const gateway = await startGateway(["--config", gatewayConfig, "serve", "--no-management"]);
  gateways.push(gateway);
  await waitForTLS(address, gateway.process);
  return { address, certificate, directory, maximumConcurrentRequests: () => maximum };
}

function waitForTLS(address: string, process: ChildProcess): Promise<void> {
  return new Promise((resolve, reject) => {
    const [host, port] = address.split(":");
    const deadline = Date.now() + 5_000;
    const connect = () => {
      const connection = tls.connect({ host, port: Number(port), rejectUnauthorized: false });
      connection.once("secureConnect", () => {
        connection.destroy();
        resolve();
      });
      connection.once("error", (error) => {
        connection.destroy();
        if (process.exitCode !== null) reject(new Error(`Gateway exited before TLS became ready (${process.exitCode})`));
        else if (Date.now() >= deadline) reject(new Error(`TLS listener did not become ready: ${error.message}`));
        else setTimeout(connect, 25);
      });
    };
    connect();
  });
}

async function runClient(address: string, certificate: string, mode: string, idleDeadline?: string): Promise<string> {
  const args = ["run", "./tests/fixtures/http3-limits-client", `https://${address}/`, certificate, mode];
  if (idleDeadline !== undefined) args.push(idleDeadline);
  const result = await execFileAsync("go", args, { cwd: coreRoot, timeout: 10_000 });
  return result.stdout.trim();
}

describe("HTTP/3 transport limits", () => {
  it("limits simultaneous QUIC connections", async () => {
    const gateway = await startHTTP3({ limits: 'maxConnections: 1, maxStreams: 10, maxPacketBytes: "1350", idleTimeout: 30s' });
    expect(await runClient(gateway.address, gateway.certificate, "connections")).toBe("1");
  }, 20_000);

  it("limits concurrent request streams per QUIC connection", async () => {
    const gateway = await startHTTP3({ limits: 'maxConnections: 10, maxStreams: 1, maxPacketBytes: "1350", idleTimeout: 30s' });
    expect(await runClient(gateway.address, gateway.certificate, "streams")).toBe("");
    expect(gateway.maximumConcurrentRequests()).toBe(1);
  }, 20_000);

  it("closes an inactive QUIC connection at idleTimeout", async () => {
    const gateway = await startHTTP3({ limits: 'maxConnections: 10, maxStreams: 10, maxPacketBytes: "1350", idleTimeout: 1s' });
    expect(await runClient(gateway.address, gateway.certificate, "idle", "2")).toBe("closed");
  }, 20_000);
});
