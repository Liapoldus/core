import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { jsonOutput, runGateway, startGateway } from "../support/gateway.js";
import { freeAddress, portOf, request, waitReady, writeGatewayConfig } from "../support/http.js";
import { startRefusingUpstream, startUpstream } from "../support/upstream.js";

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

async function startProxiedGateway(upstreams: string, routes: string): Promise<string> {
  const address = await freeAddress();
  const configPath = await writeGatewayConfig(
    [
      `upstreams:`,
      upstreams,
      `listeners:`,
      `  web:`,
      `    type: http`,
      `    address: ${address}`,
      routes,
    ].join("\n"),
  );
  const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
  gateways.push(gateway);
  await waitReady(address);
  return address;
}

function proxyRoute(prefix: string, body: string): string {
  return [
    `    routes:`,
    `      - when:`,
    `          path:`,
    `            prefix: ${prefix}`,
    `        then:`,
    `          proxy: ${body}`,
  ].join("\n");
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

describe("reverse proxy", () => {
  it("proxies a matched route to the upstream target", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startProxiedGateway(
      [`  api:`, `    targets:`, `      - address: ${upstream.address}`].join("\n"),
      proxyRoute(`/api/`, `{ upstream: api }`),
    );

    const response = await request(address, "/api/echo");

    expect(response.status).toBe(200);
    expect(response.text).toBe("upstream");
    const hitsAfterUrl = upstream.hits();
    expect(hitsAfterUrl.requests).toBe(1);
    expect(hitsAfterUrl.paths).toEqual(["/api/echo"]);
  });

  it("balances round-robin across equal-weight targets", async () => {
    const first = await startUpstream();
    const second = await startUpstream();
    servers.push(first, second);
    const targets = [`      - address: ${first.address}`, `      - address: ${second.address}`].join("\n");
    const address = await startProxiedGateway(
      [`  api:`, `    targets:`, targets].join("\n"),
      proxyRoute(`/api/`, `{ upstream: api, host: upstream }`),
    );

    for (let i = 0; i < 6; i += 1) {
      const response = await request(address, `/api/balance-${i}`);
      expect(response.status).toBe(200);
    }

    expect(first.hits().requests).toBe(3);
    expect(second.hits().requests).toBe(3);
  });

  it("retries a connect failure onto the remaining healthy target", async () => {
    const refusing = await startRefusingUpstream();
    const healthy = await startUpstream();
    servers.push(refusing, healthy);
    const targets = [
      `      - address: ${refusing.address}`,
      `      - address: ${healthy.address}`,
      `    retry:`,
      `      attempts: 1`,
      `      on: [connect-failure]`,
    ].join("\n");
    const address = await startProxiedGateway([`  api:`, `    targets:`, targets].join("\n"), proxyRoute(`/api/`, `{ upstream: api }`));

    for (let i = 0; i < 4; i += 1) {
      const response = await request(address, `/api/retry-${i}`);
      expect(response.status).toBe(200);
    }

    expect(healthy.hits().requests).toBe(4);
    expect(refusing.connections()).toBeGreaterThanOrEqual(1);
  });

  it("sets forwarded headers for the upstream", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startProxiedGateway(
      [`  api:`, `    targets:`, `      - address: ${upstream.address}`].join("\n"),
      proxyRoute(`/api/`, `{ upstream: api }`),
    );

    await request(address, "/api/forwarded", {
      headers: { "x-forwarded-host": "spoof.example", "x-forwarded-proto": "https" },
    });

    const header = upstream.hits().headers[0];
    expect(header["x-forwarded-host"]).toBe(address);
    expect(header["x-forwarded-proto"]).toBe("http");
    expect(header["x-forwarded-port"]).toBe(portOf(address));
    expect(header["x-forwarded-for"]).toBe("127.0.0.1");
    expect(header.host).toBe(address);
  });

  it("compiles and serves an upstream using hash balancing", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startProxiedGateway(
      [
        `  api:`,
        `    targets:`,
        `      - address: ${upstream.address}`,
        `    balance: hash`,
        `    hash:`,
        `      source: source-ip`,
      ].join("\n"),
      proxyRoute(`/api/`, `{ upstream: api }`),
    );

    const response = await request(address, "/api/hash");
    expect(response.status).toBe(200);
    expect(response.text).toBe("upstream");
    expect(upstream.hits().requests).toBe(1);
  });

  it("serves the scalar proxy name shortcut", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startProxiedGateway(
      [`  api:`, `    targets:`, `      - address: ${upstream.address}`].join("\n"),
      proxyRoute(`/api/`, `api`),
    );

    const response = await request(address, "/api/scalar");
    expect(response.status).toBe(200);
    expect(upstream.hits().requests).toBe(1);
  });

  it("compiles the documented host modes preserve and upstream (regression)", async () => {
    for (const host of ["preserve", "upstream"]) {
      const configPath = await writeGatewayConfig(
        [
          `upstreams:`,
          `  api:`,
          `    targets:`,
          `      - address: 127.0.0.1:1`,
          `listeners:`,
          `  web:`,
          `    type: http`,
          `    address: 127.0.0.1:0`,
          `    routes:`,
          `      - when:`,
          `          path:`,
          `            prefix: /api/`,
          `        then:`,
          `          proxy: { upstream: api, host: ${host} }`,
        ].join("\n"),
      );
      const result = await runGateway(["--output", "json", "--config", configPath, "config", "validate"]);
      expect(result.exitCode).toBe(0);
      expect(jsonOutput(result)).toMatchObject({ valid: true });
    }
  });

  it("fails serve for a proxy route whose upstream is not defined", async () => {
    const configPath = await writeGatewayConfig(
      [
        `listeners:`,
        `  web:`,
        `    type: http`,
        `    address: 127.0.0.1:0`,
        `    routes:`,
        `      - when:`,
        `          path:`,
        `            prefix: /api/`,
        `        then:`,
        `          proxy: { upstream: does-not-exist }`,
      ].join("\n"),
    );

    expect(await serveExit(configPath)).toBe(3);

    const result = await runGateway(["--output", "json", "--config", configPath, "serve", "--no-management"]);
    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_invalid" } });
  });

  it("fails serve for an upstream target address that is invalid", async () => {
    const configPath = await writeGatewayConfig(
      [
        `upstreams:`,
        `  api:`,
        `    targets:`,
        `      - address: "://invalid"`,
        `listeners:`,
        `  web:`,
        `    type: http`,
        `    address: 127.0.0.1:0`,
        `    routes:`,
        `      - when:`,
        `          path:`,
        `            prefix: /api/`,
        `        then:`,
        `          proxy: { upstream: api }`,
      ].join("\n"),
    );

    expect(await serveExit(configPath)).toBe(3);

    const result = await runGateway(["--output", "json", "--config", configPath, "serve", "--no-management"]);
    expect(result.exitCode).toBe(3);
    expect(jsonOutput(result)).toMatchObject({ problem: { code: "config_invalid" } });
  });
});