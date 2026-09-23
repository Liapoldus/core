import { createServer, type Server } from "node:http";
import dgram, { type RemoteInfo, type Socket } from "node:dgram";
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
import { freeAddress, portOf } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const sockets: Socket[] = [];
const upstreams: Server[] = [];
const directories: string[] = [];

afterEach(async () => {
  await Promise.all(gateways.splice(0).map((gateway) => gateway.stop()));
  await Promise.all(sockets.splice(0).map((socket) => new Promise<void>((resolve) => socket.close(() => resolve()))));
  await Promise.all(upstreams.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))));
  await Promise.all(directories.splice(0).map((directory) => rm(directory, { recursive: true, force: true })));
});

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

async function startUDPProbe(gatewayAddress: string): Promise<{
  address: string;
  maximumServerDatagram(): number;
}> {
  const [gatewayHost, gatewayPortText] = gatewayAddress.split(":");
  const gatewayPort = Number(gatewayPortText);
  const socket = dgram.createSocket("udp4");
  sockets.push(socket);
  let client: Pick<RemoteInfo, "address" | "port"> | undefined;
  let maximum = 0;
  await new Promise<void>((resolve, reject) => {
    socket.once("error", reject);
    socket.bind(0, "127.0.0.1", resolve);
  });
  socket.on("message", (packet, remote) => {
    if (remote.port === gatewayPort) {
      maximum = Math.max(maximum, packet.length);
      if (client !== undefined) socket.send(packet, client.port, client.address);
      return;
    }
    client = { address: remote.address, port: remote.port };
    socket.send(packet, gatewayPort, gatewayHost);
  });
  const address = socket.address();
  return {
    address: `127.0.0.1:${address.port}`,
    maximumServerDatagram: () => maximum,
  };
}

describe("HTTP/3 QUIC packet limit", () => {
  it("caps server UDP datagrams at listener.limits.quic.maxPacketBytes", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-http3-packet-limit-"));
    directories.push(directory);
    const certificate = join(directory, "server.crt");
    const key = join(directory, "server.key");
    await execFileAsync("openssl", [
      "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
      "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost", "-keyout", key, "-out", certificate,
    ]);

    const payload = "x".repeat(64 * 1024);
    const upstream = createServer((_request, response) => response.end(payload));
    upstreams.push(upstream);
    const upstreamAddress = await new Promise<string>((resolve, reject) => {
      upstream.once("error", reject);
      upstream.listen(0, "127.0.0.1", () => {
        const address = upstream.address();
        if (address === null || typeof address === "string") reject(new Error("upstream did not bind a TCP address"));
        else resolve(`127.0.0.1:${address.port}`);
      });
    });

    const gatewayAddress = await freeAddress();
    const gatewayConfig = join(directory, "gateway.yaml");
    await writeFile(gatewayConfig, [
      "tlsProfiles:",
      "  public:",
      "    certificates:",
      `      - { cert: ${JSON.stringify(certificate)}, key: ${JSON.stringify(key)} }`,
      "    protocols: [http/1.1, h2, h3]",
      "upstreams:",
      "  app:",
      "    targets:",
      `      - address: ${upstreamAddress}`,
      "listeners:",
      "  public:",
      "    type: http",
      `    address: ${gatewayAddress}`,
      "    tls: public",
      '    limits: { quic: { maxPacketBytes: "1200" } }',
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { proxy: { upstream: app } }",
    ].join("\n"), "utf8");

    const gateway = await startGateway(["--config", gatewayConfig, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitForTLS(gatewayAddress, gateway.process);
    const probe = await startUDPProbe(gatewayAddress);
    const response = await execFileAsync("go", [
      "run", "./tests/fixtures/http3-client", `https://${probe.address}/`, certificate,
    ], { cwd: coreRoot });

    expect(response.stdout.trim()).toBe("3 200");
    expect(probe.maximumServerDatagram()).toBeGreaterThan(0);
    expect(probe.maximumServerDatagram()).toBeLessThanOrEqual(1200);
  });
});
