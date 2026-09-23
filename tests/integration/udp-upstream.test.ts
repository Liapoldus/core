import dgram from "node:dgram";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { startGateway } from "../support/gateway.js";
import { writeGatewayConfig } from "../support/http.js";

const vectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{
    id: string;
    input: { listener: string; upstream: string; datagramHex: string };
    expected: { transport: string; datagramPreserved: boolean };
  }>;
};
const vector = vectors.vectors.find(({ id }) => id === "udp-upstream-protocol");
if (!vector) throw new Error("udp-upstream-protocol vector is missing");

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const sockets: Array<dgram.Socket> = [];

async function bindUdp(socket: dgram.Socket): Promise<number> {
  await new Promise<void>((resolve, reject) => {
    socket.once("error", reject);
    socket.bind(0, "127.0.0.1", () => {
      socket.removeListener("error", reject);
      resolve();
    });
  });
  const address = socket.address();
  return address.port;
}

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (!gateway.process.killed) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
  for (const socket of sockets.splice(0)) {
    await new Promise<void>((resolve) => socket.close(() => resolve()));
  }
});

describe("UDP upstream golden vector", () => {
  it("forwards one opaque datagram without changing its bytes", async () => {
    expect(vector.input.listener).toBe("udp");
    expect(vector.expected).toEqual({ transport: "udp", datagramPreserved: true });
    const datagram = Buffer.from(vector.input.datagramHex, "hex");
    const upstream = dgram.createSocket("udp4");
    sockets.push(upstream);
    const received: Buffer[] = [];
    upstream.on("message", (payload, remote) => {
      received.push(Buffer.from(payload));
      upstream.send(payload, remote.port, remote.address);
    });
    const upstreamPort = await bindUdp(upstream);

    const listener = dgram.createSocket("udp4");
    sockets.push(listener);
    const listenerPort = await bindUdp(listener);
    await new Promise<void>((resolve) => listener.close(() => resolve()));
    sockets.pop();

    const config = await writeGatewayConfig([
      "upstreams:",
      `  ${vector.input.upstream}:`,
      "    targets:",
      `      - address: 127.0.0.1:${upstreamPort}`,
      "listeners:",
      "  relay:",
      `    type: ${vector.input.listener}`,
      `    address: 127.0.0.1:${listenerPort}`,
      "    rules:",
      "      - then:",
      `          proxy: { upstream: ${vector.input.upstream} }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);

    const response = await new Promise<Buffer>((resolve, reject) => {
      const client = dgram.createSocket("udp4");
      sockets.push(client);
      const timeout = setTimeout(() => {
        clearInterval(retry);
        reject(new Error("Gateway did not return the UDP datagram"));
      }, 8_000);
      client.once("message", (payload) => {
        clearTimeout(timeout);
        clearInterval(retry);
        resolve(Buffer.from(payload));
      });
      const retry = setInterval(() => client.send(datagram, listenerPort, "127.0.0.1"), 100);
      client.send(datagram, listenerPort, "127.0.0.1");
    });

    expect(received[0]).toEqual(datagram);
    expect(response).toEqual(datagram);
  }, 20_000);
});
