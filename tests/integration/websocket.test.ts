import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, waitReady, writeGatewayConfig } from "../support/http.js";
import { startEchoWebSocketServer, websocketEcho } from "../support/websocket.js";

const gateways: Array<{ process: ChildProcess; stop: () => Promise<void> }> = [];
const servers: Array<{ stop: () => Promise<void> }> = [];
const goldenVectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{ id: string; input: { upgrade?: string }; expected: { upgraded?: boolean } }>;
};

function vector(id: string) {
  const match = goldenVectors.vectors.find((candidate) => candidate.id === id);
  if (match === undefined) throw new Error(`missing gateway golden vector ${id}`);
  return match;
}

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    await gateway.stop();
  }
  for (const server of servers.splice(0)) {
    await server.stop();
  }
});

describe("websocket proxying", () => {
  it("proxies a websocket upgrade and echoes frames through the gateway", async () => {
    const expected = vector("proxy-websocket");
    const ws = await startEchoWebSocketServer();
    servers.push(ws);

    const address = await freeAddress();
    const configPath = await writeGatewayConfig(
      [
        `upstreams:`,
        `  ws:`,
        `    targets:`,
        `      - address: ${ws.url}`,
        `listeners:`,
        `  web:`,
        `    type: http`,
        `    address: ${address}`,
        `    routes:`,
        `      - when:`,
        `          path:`,
        `            prefix: /echo`,
        `        then:`,
        `          proxy:`,
        `            upstream: ws`,
        `            host: upstream`,
      ].join("\n"),
    );

    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    await expect(websocketEcho(address, "/echo", "ping")).resolves.toBe("ping");
    expect(expected.input.upgrade).toBe("websocket");
    expect(expected.expected.upgraded).toBe(true);
  });

  it("returns 404 for a websocket upgrade on an unmatched path", async () => {
    const ws = await startEchoWebSocketServer();
    servers.push(ws);

    const address = await freeAddress();
    const configPath = await writeGatewayConfig(
      [
        `upstreams:`,
        `  ws:`,
        `    targets:`,
        `      - address: ${ws.url}`,
        `listeners:`,
        `  web:`,
        `    type: http`,
        `    address: ${address}`,
        `    routes:`,
        `      - when:`,
        `          path:`,
        `            prefix: /echo`,
        `        then:`,
        `          proxy:`,
        `            upstream: ws`,
        `            host: upstream`,
      ].join("\n"),
    );

    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    await expect(websocketEcho(address, "/other", "ping")).rejects.toThrow(/expected 101 upgrade/);
  });
});
