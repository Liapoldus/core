import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";
import { startUpstream } from "../support/upstream.js";

interface Handle {
  stop: () => Promise<void>;
}

const gateways: Array<{ process: ChildProcess } & Handle> = [];
const servers: Array<Handle> = [];

async function cleanup(handle: { process: ChildProcess } & Handle): Promise<void> {
  if (!handle.process.killed) {
    handle.process.kill("SIGTERM");
  }
  await handle.stop();
}

afterEach(async () => {
  for (const gateway of gateways.splice(0)) {
    await cleanup(gateway);
  }
  for (const server of servers.splice(0)) {
    await server.stop();
  }
});

function configTemplate(thenBody: string, upstreamTarget?: string): Promise<string> {
  return writeGatewayConfig(
    [
      `sites:`,
      `  main:`,
      `    source:`,
      `      type: directory`,
      `      root: /tmp/liapoldus-exclusivity-site`,
      `upstreams:`,
      `  api:`,
      `    targets:`,
      upstreamTarget ?? `      - address: 127.0.0.1:1`,
      `listeners:`,
      `  web:`,
      `    type: http`,
      `    address: 127.0.0.1:0`,
      `    routes:`,
      `      - when:`,
      `          path:`,
      `            prefix: /t/`,
      `        then:`,
      thenBody,
    ].join("\n"),
  );
}

async function serveExit(configPath: string): Promise<number | null> {
  const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      gateway.process.kill("SIGKILL");
      reject(new Error("serve did not exit within 5s"));
    }, 5000);
    gateway.process.once("close", (code, signal) => {
      clearTimeout(timer);
      resolve(signal === null ? code : null);
    });
  });
}

describe("route terminal action exclusivity", () => {
  it("rejects a route with both site and proxy actions", async () => {
    const configPath = await configTemplate([
      `          site: main`,
      `          proxy: { upstream: api }`,
    ].join("\n"));

    expect(await serveExit(configPath)).toBe(3);
  });

  it("rejects a route with both site and redirect actions", async () => {
    const configPath = await configTemplate([
      `          site: main`,
      `          redirect: { path: /elsewhere }`,
    ].join("\n"));

    expect(await serveExit(configPath)).toBe(3);
  });

  it("rejects a route with both proxy and redirect actions", async () => {
    const configPath = await configTemplate([
      `          proxy: { upstream: api }`,
      `          redirect: { path: /elsewhere }`,
    ].join("\n"));

    expect(await serveExit(configPath)).toBe(3);
  });

  it("serves a proxy terminal together with rewrite and headers transforms", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await freeAddress();
    const configPath = await writeGatewayConfig(
      [
        `upstreams:`,
        `  api:`,
        `    targets:`,
        `      - address: ${upstream.address}`,
        `listeners:`,
        `  web:`,
        `    type: http`,
        `    address: ${address}`,
        `    routes:`,
        `      - when:`,
        `          path:`,
        `            prefix: /t/`,
        `        then:`,
        `          proxy: { upstream: api }`,
        `          rewrite: { regex: '^/t/(.*)$', replacement: '/x/$1' }`,
        `          headers: { request: { set: { x-gateway: liapoldus } } }`,
      ].join("\n"),
    );
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/t/entry");

    expect(response.status).toBe(200);
    expect(upstream.hits().paths).toEqual(["/x/entry"]);
    expect(upstream.hits().headers[0]["x-gateway"]).toBe("liapoldus");
  });

  it("serves a redirect as the sole terminal action", async () => {
    const address = await freeAddress();
    const configPath = await writeGatewayConfig(
      [
        `listeners:`,
        `  web:`,
        `    type: http`,
        `    address: ${address}`,
        `    routes:`,
        `      - when:`,
        `          path:`,
        `            prefix: /t/`,
        `        then:`,
        `          redirect: { path: /x }`,
      ].join("\n"),
    );
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/t/a", { redirect: "manual" });

    expect(response.status).toBe(308);
  });
});