import dgram from "node:dgram";
import { execFile } from "node:child_process";
import { mkdtemp } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, writeGatewayConfig } from "../support/http.js";

const execFileAsync = promisify(execFile);
const root = fileURLToPath(new URL("../..", import.meta.url));
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

    const payload = Buffer.from([0, 171, 255]);
    const response = await exchangeUntilReady(address, payload);
    expect(response).toEqual(Buffer.concat([Buffer.from("stream:"), payload]));
  }, 60_000);
});
