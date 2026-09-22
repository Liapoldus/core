import { afterEach, describe, expect, it } from "vitest";
import net from "node:net";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, writeGatewayConfig } from "../support/http.js";

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const servers: net.Server[] = [];

afterEach(async () => {
  await Promise.all(gateways.splice(0).map((gateway) => gateway.stop()));
  await Promise.all(servers.splice(0).map((server) => new Promise<void>((resolve) => server.close(() => resolve()))));
});

function listen(server: net.Server): Promise<string> {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (!address || typeof address === "string") return reject(new Error("missing address"));
      resolve(`127.0.0.1:${address.port}`);
    });
  });
}

function exchange(address: string, message: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const [host, port] = address.split(":");
    const socket = net.createConnection({ host, port: Number(port) });
    let result = "";
    socket.once("error", reject);
    socket.on("data", (chunk) => { result += chunk.toString(); });
    socket.once("end", () => resolve(result));
    socket.once("connect", () => socket.end(message));
  });
}

describe("L4 runtime", () => {
  it("relays a TCP stream to the configured upstream", async () => {
    const upstream = net.createServer((socket) => socket.on("data", (data) => socket.end(Buffer.from(`echo:${data}`))));
    servers.push(upstream);
    const upstreamAddress = await listen(upstream);
    const gatewayAddress = await freeAddress();
    const config = await writeGatewayConfig([
      "upstreams:", `  echo:`, "    targets:", `      - address: ${upstreamAddress}`,
      "listeners:", "  tcp:", "    type: tcp", `    address: ${gatewayAddress}`, "    rules:", "      - then:", "          proxy: echo",
    ].join("\n"));
    const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
    gateways.push(gateway);
    await new Promise((resolve) => setTimeout(resolve, 500));
    const response = await exchange(gatewayAddress, "hello");
    expect(response).toBe("echo:hello");
  });
});
