import dgram from "node:dgram";
import { execFile } from "node:child_process";
import { readFileSync } from "node:fs";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, writeGatewayConfig } from "../support/http.js";

const execFileAsync = promisify(execFile);
const root = fileURLToPath(new URL("../..", import.meta.url));
const vectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{
    id: string;
    input: { payloadHex: string };
    expected: { oneDatagram: boolean; payloadEncoding: string };
  }>;
};
const vector = vectors.vectors.find(({ id }) => id === "plugin-stream-udp-datagram");
if (!vector) throw new Error("plugin-stream-udp-datagram vector is missing");
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    if (gateway.process.exitCode === null) gateway.process.kill("SIGTERM");
    await gateway.stop();
  }
});

async function exchangeUntilReady(address: string, payload: Buffer): Promise<Buffer> {
  const port = Number(address.split(":").at(-1));
  const socket = dgram.createSocket("udp4");
  return new Promise<Buffer>((resolve, reject) => {
    const timeout = setTimeout(() => {
      clearInterval(retry);
      socket.close();
      reject(new Error("Gateway UDP plugin listener did not respond"));
    }, 10_000);
    const retry = setInterval(() => socket.send(payload, port, "127.0.0.1"), 100);
    socket.once("message", (response) => {
      clearTimeout(timeout);
      clearInterval(retry);
      socket.close();
      resolve(Buffer.from(response));
    });
    socket.once("error", (error) => {
      clearTimeout(timeout);
      clearInterval(retry);
      socket.close();
      reject(error);
    });
  });
}

async function exchangeOnce(address: string, payload: Buffer): Promise<Buffer> {
  const port = Number(address.split(":").at(-1));
  const socket = dgram.createSocket("udp4");
  return new Promise<Buffer>((resolve, reject) => {
    const timeout = setTimeout(() => {
      socket.close();
      reject(new Error("Gateway UDP plugin listener did not respond to the datagram"));
    }, 2_000);
    socket.once("message", (response) => {
      clearTimeout(timeout);
      socket.close();
      resolve(Buffer.from(response));
    });
    socket.once("error", (error) => {
      clearTimeout(timeout);
      socket.close();
      reject(error);
    });
    socket.send(payload, port, "127.0.0.1");
  });
}

describe("Gateway gRPC plugin UDP lifecycle", () => {
  it("uses a typed Stream lifecycle per datagram and preserves raw bytes", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-grpc-plugin-udp-"));
    const binary = join(directory, "forms-plugin");
    await execFileAsync("go", ["build", "-o", binary, "./tests/fixtures/plugin-grpc"], { cwd: root });
    const address = await freeAddress();
    const config = await writeGatewayConfig([
      "plugins:",
      "  forms:",
      `    binary: ${JSON.stringify(binary)}`,
      "    capabilities: [tcp.echo]",
      "    settings: {}",
      "listeners:",
      "  datagrams:",
      "    type: udp",
      `    address: ${address}`,
      "    rules:",
      "      - then:",
      "          plugin: { instance: forms, capability: tcp.echo }",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);

    expect(vector.expected).toEqual({ oneDatagram: true, payloadEncoding: "raw-bytes" });
    const payload = Buffer.from(vector.input.payloadHex, "hex");
    const firstResponse = await exchangeUntilReady(address, payload);
    expect(firstResponse).toEqual(Buffer.concat([Buffer.from("stream:"), payload]));

    const secondPayload = Buffer.from([255, 0, 127]);
    const secondResponse = await exchangeOnce(address, secondPayload);
    expect(secondResponse).toEqual(Buffer.concat([Buffer.from("stream:"), secondPayload]));
  }, 60_000);
});
