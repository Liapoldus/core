import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { request as httpsRequest } from "node:https";
import type { ChildProcess } from "node:child_process";
import { afterEach, describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { startGateway } from "../support/gateway.js";
import { freeAddress, waitReady, writeGatewayConfig } from "../support/http.js";
import { startUpstream } from "../support/upstream.js";

const execFileAsync = promisify(execFile);
const vectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{
    id: string;
    input: { host: string; peerIp: string; scheme: string; port: number; headers: Record<string, string> };
    expected: { upstreamHost: string; stripped: string[]; headers: Record<string, string> };
  }>;
};
const vector = vectors.vectors.find(({ id }) => id === "proxy-forwarding");
if (!vector) throw new Error("proxy-forwarding vector is missing");

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const upstreams: Array<{ stop(): Promise<void> }> = [];
const directories: string[] = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const upstream of upstreams.splice(0)) await upstream.stop();
  for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true });
});

describe("proxy-forwarding golden vector", () => {
  it("overwrites spoofed forwarding headers and strips hop-by-hop headers", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-proxy-forwarding-"));
    directories.push(directory);
    const certificate = join(directory, "server.crt");
    const key = join(directory, "server.key");
    await execFileAsync("openssl", [
      "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
      "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost", "-keyout", key, "-out", certificate,
    ]);

    const upstream = await startUpstream();
    upstreams.push(upstream);
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "tlsProfiles:",
      "  public:",
      "    certificates:",
      `      - { cert: ${JSON.stringify(certificate)}, key: ${JSON.stringify(key)} }`,
      "    protocols: [http/1.1, h2]",
      "upstreams:",
      "  api:",
      "    targets:",
      `      - address: ${upstream.address}`,
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${address}`,
      "    tls: public",
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { proxy: { upstream: api } }",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const [, gatewayPort] = address.split(":");
    const response = await new Promise<{ status: number | undefined }>((resolve, reject) => {
      const request = httpsRequest({
        hostname: "127.0.0.1",
        port: gatewayPort,
        path: "/forwarded",
        method: "GET",
        rejectUnauthorized: false,
        headers: {
          host: vector.input.host,
          connection: vector.input.headers.Connection,
          "x-forwarded-host": vector.input.headers["X-Forwarded-Host"],
        },
      }, (incoming) => {
        incoming.resume();
        incoming.once("end", () => resolve({ status: incoming.statusCode }));
      });
      request.once("error", reject);
      request.end();
    });

    expect(response.status).toBe(200);
    const headers = upstream.hits().headers[0];
    expect(headers.host).toBe(vector.expected.upstreamHost);
    for (const name of vector.expected.stripped) expect(headers[name.toLowerCase()]).toBeUndefined();
    expect(headers["x-forwarded-for"]).toBe("127.0.0.1");
    expect(headers["x-forwarded-host"]).toBe(vector.expected.headers["X-Forwarded-Host"]);
    expect(headers["x-forwarded-proto"]).toBe(vector.expected.headers["X-Forwarded-Proto"]);
    expect(headers["x-forwarded-port"]).toBe(gatewayPort);
  });
});
