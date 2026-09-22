import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { startGateway } from "../support/gateway.js";
import { freeAddress, portOf, request, waitReady, writeGatewayConfig } from "../support/http.js";
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

async function startActionsGateway(routes: string, upstreamTargets?: string): Promise<string> {
  const address = await freeAddress();
  const configPath = await writeGatewayConfig(
    [
      `upstreams:`,
      `  api:`,
      `    targets:`,
      upstreamTargets ?? `      - address: 127.0.0.1:1`,
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

function proxyRoute(prefix: string, extraLine: string): string {
  return [
    `    routes:`,
    `      - when:`,
    `          path:`,
    `            prefix: ${prefix}`,
    `        then:`,
    `          proxy: { upstream: api }`,
    extraLine,
  ].join("\n");
}

function redirectRoute(prefix: string, action: string): string {
  return [
    `    routes:`,
    `      - when:`,
    `          path:`,
    `            prefix: ${prefix}`,
    `        then:`,
    `          redirect: ${action}`,
  ].join("\n");
}

function denyRoute(prefix: string, action = `{ status: 403, code: forbidden }`): string {
  return [
    `    routes:`,
    `      - when:`,
    `          path:`,
    `            prefix: ${prefix}`,
    `        then:`,
    `          deny: ${action}`,
  ].join("\n");
}

describe("route deny actions", () => {
  it("returns the configured denial status instead of silently becoming a 404", async () => {
    const address = await startActionsGateway(denyRoute(`/private`, `{ status: 451, code: policy_denied }`));
    const response = await request(address, "/private/data");
    expect(response.status).toBe(451);
  });
});

describe("WAF policy limit action", () => {
  it("uses the named token bucket and stops before proxying on exhaustion", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await freeAddress();
    const configPath = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `rateLimits:`, `  api: { key: source-ip, requests: 1, per: 1m, burst: 1 }`,
      `wafPolicies:`, `  public:`, `    rules:`,
      `      - when: { path: { prefix: /api } }`, `        then: { limit: api }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
      `    routes:`, `      - when: { path: { prefix: /api } }`,
      `        then: { proxy: api, waf: public }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const first = await request(address, "/api/first");
    const second = await request(address, "/api/second");
    expect(first.status).toBe(200);
    expect(second.status).toBe(429);
    expect(second.headers.get("retry-after")).toBe("60");
    expect(upstream.hits().paths).toEqual(["/api/first"]);
  });
});

describe("plugin route actions", () => {
  it("does not silently turn a declared plugin action into a 404", async () => {
    const address = await startActionsGateway([
      `    routes:`,
      `      - when:`,
      `          path:`,
      `            prefix: /plugin`,
      `        then:`,
      `          plugin: { instance: forms, capability: forms.submit }`,
    ].join("\n"));
    const response = await request(address, "/plugin/submit");
    expect(response.status).toBe(503);
  });
});

describe("route redirect actions", () => {
  it("redirects with the default 308 status and preserves the query by default", async () => {
    const address = await startActionsGateway(redirectRoute(`/old`, `{ path: /new }`));

    const response = await request(address, "/old?a=1&b=2", { redirect: "manual" });

    expect(response.status).toBe(308);
    expect(response.headers.get("location")).toBe(`http://${address}/new?a=1&b=2`);
  });

  it("redirects with an explicit 301 status and an overridden path", async () => {
    const address = await startActionsGateway(redirectRoute(`/old`, `{ path: /new, status: 301 }`));

    const response = await request(address, "/old", { redirect: "manual" });

    expect(response.status).toBe(301);
    expect(response.headers.get("location")).toBe(`http://${address}/new`);
  });

  it("honors every documented redirect status", async () => {
    for (const status of [301, 302, 307, 308]) {
      const address = await startActionsGateway(redirectRoute(`/old`, `{ path: /new, status: ${status} }`));

      const response = await request(address, "/old", { redirect: "manual" });
      expect(response.status).toBe(status);
    }
  });

  it("drops the query when preserveQuery is false", async () => {
    const address = await startActionsGateway(redirectRoute(`/old`, `{ path: /new, preserveQuery: false }`));

    const response = await request(address, "/old?a=1", { redirect: "manual" });

    expect(response.status).toBe(308);
    expect(response.headers.get("location")).toBe(`http://${address}/new`);
  });

  it("upgrades the scheme to https keeping the request host", async () => {
    const address = await startActionsGateway(redirectRoute(`/old`, `{ scheme: https }`));

    const response = await request(address, "/old", { redirect: "manual" });

    expect(response.status).toBe(308);
    expect(response.headers.get("location")).toBe(`https://${address}/old`);
  });

  it("overrides the host and the path", async () => {
    const address = await startActionsGateway(redirectRoute(`/old`, `{ host: example.com, path: /landing }`));

    const response = await request(address, "/old", { redirect: "manual" });

    expect(response.status).toBe(308);
    expect(response.headers.get("location")).toBe("http://example.com/landing");
  });
});

describe("route rewrite actions", () => {
  it("rewrites the request path before proxying, keeping captures and the query", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startActionsGateway(
      proxyRoute(`/old/`, `          rewrite: { regex: '^/old/(.*)$', replacement: '/new/\${1}' }`),
      `      - address: ${upstream.address}`,
    );

    const response = await request(address, "/old/a/b?q=1");

    expect(response.status).toBe(200);
    expect(upstream.hits().paths).toEqual(["/new/a/b?q=1"]);
  });

  it("rewrites with a static replacement path", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startActionsGateway(
      proxyRoute(`/legacy`, `          rewrite: { regex: '.*', replacement: '/home' }`),
      `      - address: ${upstream.address}`,
    );

    const response = await request(address, "/legacy/entry");

    expect(response.status).toBe(200);
    expect(upstream.hits().paths).toEqual(["/home"]);
  });
});

describe("route header actions", () => {
  it("sets a request header the upstream sees", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startActionsGateway(
      proxyRoute(`/api/`, `          headers: { request: { set: { x-gateway: liapoldus } } }`),
      `      - address: ${upstream.address}`,
    );

    await request(address, "/api/header");

    expect(upstream.hits().headers[0]["x-gateway"]).toBe("liapoldus");
  });

  it("setIfAbsent adds a missing header but never replaces a gateway-owned one", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startActionsGateway(
      proxyRoute(
        `/api/`,
        `          headers: { request: { setIfAbsent: { x-forwarded-port: spoof, x-new: added } } }`,
      ),
      `      - address: ${upstream.address}`,
    );

    await request(address, "/api/header");

    const header = upstream.hits().headers[0];
    expect(header["x-forwarded-port"]).toBe(portOf(address));
    expect(header["x-new"]).toBe("added");
  });

  it("deletes a request header the client sent", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startActionsGateway(
      proxyRoute(`/api/`, `          headers: { request: { delete: [x-secret] } }`),
      `      - address: ${upstream.address}`,
    );

    await request(address, "/api/header", { headers: { "x-secret": "keep-out" } });

    expect(upstream.hits().headers[0]["x-secret"]).toBeUndefined();
  });

  it("sets a response header the client sees", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startActionsGateway(
      proxyRoute(`/api/`, `          headers: { response: { set: { x-from: gateway } } }`),
      `      - address: ${upstream.address}`,
    );

    const response = await request(address, "/api/header");

    expect(response.headers.get("x-from")).toBe("gateway");
  });

  it("setIfAbsent and delete apply to upstream response headers", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await startActionsGateway(
      proxyRoute(
        `/api/`,
        `          headers: { response: { setIfAbsent: { x-upstream: replace-me }, delete: [x-upstream] } }`,
      ),
      `      - address: ${upstream.address}`,
    );

    const response = await request(address, "/api/header");

    expect(response.headers.get("x-upstream")).toBeNull();
  });
});
