import { copyFile, mkdtemp } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { runGateway, startGateway } from "../support/gateway.js";
import { freeAddress, portOf, request, waitReady, writeGatewayConfig } from "../support/http.js";
import { startUpstream } from "../support/upstream.js";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

interface Handle {
  stop: () => Promise<void>;
}

const gateways: Array<{ process: ChildProcess } & Handle> = [];
const servers: Array<Handle> = [];
const gatewayVectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{
    id: string;
    input: { action?: { limit?: string }; bucket?: { requests: number; per: string; burst: number } | string; cors?: { origins: string[]; methods: string[] }; dataProvider?: string; method?: string; onError?: string; origin?: string; requestMethod?: string };
    expected: { code?: string; secondStatus?: number; status: number; terminalCalled?: boolean; headers?: Record<string, string> };
  }>;
};

function vector(id: string) {
  const match = gatewayVectors.vectors.find((candidate) => candidate.id === id);
  if (match === undefined) throw new Error(`missing gateway golden vector ${id}`);
  return match;
}

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

describe("CORS preflight matching", () => {
	it("satisfies the cors-preflight golden vector before calling the terminal", async () => {
		const expected = vector("cors-preflight");
		const upstream = await startUpstream();
		servers.push(upstream);
		const address = await startActionsGateway(
			[
				`    routes:`,
				`      - when: { path: { prefix: /api } }`,
				`        then:`,
				`          proxy: { upstream: api }`,
				`          cors: { origins: [${expected.input.cors!.origins.join(", ")}], methods: [${expected.input.cors!.methods.join(", ")}] }`,
			].join("\n"),
			`      - address: ${upstream.address}`,
		);
		const baseline = upstream.hits().requests;
		const response = await request(address, "/api/resource", {
			method: expected.input.method!,
			headers: { Origin: expected.input.origin!, "Access-Control-Request-Method": expected.input.requestMethod! },
		});
		expect(response.status).toBe(expected.expected.status);
		expect(response.headers.get("access-control-allow-origin")).toBe(expected.expected.headers?.["Access-Control-Allow-Origin"]);
		expect(upstream.hits().requests - baseline).toBe(Number(expected.expected.terminalCalled));
	});

	it("only short-circuits a configured origin and requested method", async () => {
		const upstream = await startUpstream();
		servers.push(upstream);
		const address = await startActionsGateway(
			[
				`    routes:`,
				`      - when: { path: { prefix: /api } }`,
				`        then:`,
				`          proxy: { upstream: api }`,
				`          cors: { origins: [https://app.example], methods: [POST] }`,
			].join("\n"),
			`      - address: ${upstream.address}`,
		);

		const invalidOrigin = await request(address, "/api/resource", {
			method: "OPTIONS",
			headers: { Origin: "https://evil.example", "Access-Control-Request-Method": "POST" },
		});
		const invalidMethod = await request(address, "/api/resource", {
			method: "OPTIONS",
			headers: { Origin: "https://app.example", "Access-Control-Request-Method": "DELETE" },
		});

		expect(invalidOrigin.status).toBe(200);
		expect(invalidOrigin.headers.get("access-control-allow-origin")).toBeNull();
		expect(invalidMethod.status).toBe(200);
		expect(invalidMethod.headers.get("access-control-allow-origin")).toBeNull();
		expect(upstream.hits().requests).toBe(2);
	});
});

describe("WAF policy limit action", () => {
  it("uses the named token bucket and stops before proxying on exhaustion", async () => {
    const expected = vector("rate-limit");
    const wafExpected = vector("waf-limit");
    const bucket = expected.input.bucket as { requests: number; per: string; burst: number };
    const limitName = wafExpected.input.action?.limit;
    if (!limitName) throw new Error("waf-limit vector has no limit action");
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await freeAddress();
    const configPath = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `rateLimits:`, `  ${limitName}: { key: source-ip, requests: ${bucket.requests}, per: ${bucket.per}, burst: ${bucket.burst} }`,
      `wafPolicies:`, `  public:`, `    rules:`,
      `      - when: { path: { prefix: /api } }`, `        then: { limit: ${limitName} }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
      `    routes:`, `      - when: { path: { prefix: /api } }`,
      `        then: { proxy: api, waf: public }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const first = await request(address, "/api/first");
    const second = await request(address, "/api/second");
    expect(first.status, first.text).toBe(200);
    expect(second.status).toBe(expected.expected.secondStatus);
    expect(second.status).toBe(wafExpected.expected.status);
    expect(second.headers.get("retry-after")).toBe(expected.expected.headers?.["Retry-After"]);
    expect(second.headers.get("content-type")).toContain("application/problem+json");
    expect(JSON.parse(second.text).code).toBe(expected.expected.code);
    expect(JSON.parse(second.text).code).toBe(wafExpected.expected.code);
    expect(upstream.hits().paths).toEqual(["/api/first"]);
  });
});

describe("WAF method matcher", () => {
  it("only applies a WAF rule when its method matcher is satisfied", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await freeAddress();
    const configPath = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  public:`, `    rules:`,
      `      - when: { method: [POST], path: { prefix: /api } }`,
      `        then: { deny: { status: 405 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
      `    routes:`, `      - when: { path: { prefix: /api } }`,
      `        then: { proxy: api, waf: public }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const get = await request(address, "/api/read");
    const post = await request(address, "/api/write", { method: "POST" });
    expect(get.status).toBe(200);
    expect(post.status).toBe(405);
    expect(upstream.hits().paths).toEqual(["/api/read"]);
  });
});

describe("composed WAF matchers", () => {
	it("evaluates nested all, any, and not conditions instead of ignoring them", async () => {
		const upstream = await startUpstream();
		servers.push(upstream);
		const address = await freeAddress();
		const configPath = await writeGatewayConfig([
			`upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
			`wafPolicies:`,
			`  all-policy:`, `    rules:`,
			`      - when: { all: [{ method: { exact: GET } }, { path: { prefix: /all } }] }`,
			`        then: { deny: { status: 451 } }`,
			`  any-policy:`, `    rules:`,
			`      - when: { any: [{ method: { exact: TRACE } }, { path: { prefix: /any-hit } }] }`,
			`        then: { deny: { status: 452 } }`,
			`  not-policy:`, `    rules:`,
			`      - when: { path: { prefix: /not }, not: { method: { exact: DELETE } } }`,
			`        then: { deny: { status: 453 } }`,
			`listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
			`    routes:`,
			`      - when: { path: { prefix: /all } }`, `        then: { proxy: { upstream: api }, waf: all-policy }`,
			`      - when: { path: { prefix: /any } }`, `        then: { proxy: { upstream: api }, waf: any-policy }`,
			`      - when: { path: { prefix: /not } }`, `        then: { proxy: { upstream: api }, waf: not-policy }`,
		].join("\n"));
		const validation = await runGateway(["--output", "json", "--config", configPath, "config", "validate"]);
		if (validation.exitCode !== 0) throw new Error(JSON.stringify(validation));
		const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
		gateways.push(gateway);
		await waitReady(address);

		const allMatch = await request(address, "/all/item", { method: "GET" });
		const allMiss = await request(address, "/all/item", { method: "POST" });
		const anyMatch = await request(address, "/any-hit/item", { method: "GET" });
		const anyMiss = await request(address, "/any-miss/item", { method: "GET" });
		const notMatch = await request(address, "/not/item", { method: "GET" });
		const notMiss = await request(address, "/not/item", { method: "DELETE" });

		expect([allMatch.status, anyMatch.status, notMatch.status]).toEqual([451, 452, 453]);
		expect([allMiss.status, anyMiss.status, notMiss.status]).toEqual([200, 200, 200]);
		expect(upstream.hits().requests).toBe(3);
	});

	it("lets a true any-branch decide the result despite an unavailable Geo branch", async () => {
		const upstream = await startUpstream();
		servers.push(upstream);
		const address = await freeAddress();
		const configPath = await writeGatewayConfig([
			`dataProviders:`, `  geo: { type: mmdb, path: /missing/GeoLite2-City.mmdb, onError: deny }`,
			`upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
			`wafPolicies:`, `  any-policy:`, `    rules:`,
			`      - when: { any: [{ geo: { provider: geo, country: { exact: US } } }, { path: { prefix: /api } }] }`,
			`        onError: deny`, `        then: { deny: { status: 451 } }`,
			`listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
			`    routes:`, `      - when: { path: { prefix: /api } }`,
			`        then: { proxy: api, waf: any-policy }`,
		].join("\n"));
		const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
		gateways.push(gateway);
		await waitReady(address);

		const response = await request(address, "/api/private");
		expect(response.status).toBe(451);
		expect(upstream.hits().requests).toBe(0);
	});
});

describe("WAF source IP matcher", () => {
  it("does not apply an in-rule when the direct peer address is outside its CIDR", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await freeAddress();
    const configPath = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  public:`, `    rules:`,
      `      - when: { sourceIp: { in: [192.0.2.0/24] }, path: { prefix: /api } }`,
      `        then: { deny: { status: 403 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
      `    routes:`, `      - when: { path: { prefix: /api } }`,
      `        then: { proxy: api, waf: public }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/api/read");
    expect(response.status).toBe(200);
    expect(upstream.hits().paths).toEqual(["/api/read"]);
  });
});

describe("WAF header and query matchers", () => {
  it("combines sibling conditions with AND and only denies matching requests", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await freeAddress();
    const configPath = await writeGatewayConfig([
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  public:`, `    rules:`,
      `      - when: { method: GET, path: { prefix: /api }, headers: { x-client: { exists: true } }, query: { role: { exact: admin } } }`,
      `        then: { deny: { status: 403 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
      `    routes:`, `      - when: { path: { prefix: /api } }`,
      `        then: { proxy: api, waf: public }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const missing = await request(address, "/api/read?role=admin");
    const partial = await request(address, "/api/read", { headers: { "x-client": "web" } });
    const matching = await request(address, "/api/read?role=admin", { headers: { "x-client": "web" } });
    expect(missing.status).toBe(200);
    expect(partial.status).toBe(200);
    expect(matching.status).toBe(403);
    expect(upstream.hits().paths).toEqual(["/api/read?role=admin", "/api/read"]);
  });
});

describe("WAF GeoIP provider failures", () => {
  it("fails closed when a referenced MMDB provider cannot be opened", async () => {
    const expected = vector("geo-provider-failure-deny");
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await freeAddress();
    const configPath = await writeGatewayConfig([
      `dataProviders:`,
      `  ${expected.input.dataProvider}: { type: mmdb, path: /missing/GeoLite2-City.mmdb, onError: ${expected.input.onError} }`,
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  geo:`, `    rules:`,
      `      - when: { geo: { provider: ${expected.input.dataProvider}, country: { exact: US } }, path: { prefix: /api } }`,
      `        then: { deny: { status: 451 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
      `    routes:`, `      - when: { path: { prefix: /api } }`,
      `        then: { proxy: api, waf: geo }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/api/private");
    expect(response.status).toBe(expected.expected.status);
    expect(response.headers.get("content-type")).toContain("application/problem+json");
    expect(JSON.parse(response.text).code).toBe(expected.expected.code);
    expect(upstream.hits().paths).toEqual([]);
  });

  it("does not turn a missing lookup reader into a successful country match", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await freeAddress();
    const configPath = await writeGatewayConfig([
      `dataProviders:`,
      `  geo: { type: mmdb, path: /missing/GeoLite2-City.mmdb, onError: deny }`,
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  geo:`, `    rules:`,
      `      - when: { geo: { provider: geo, country: { exact: US } }, path: { prefix: /api } }`,
      `        onError: allow`,
      `        then: { deny: { status: 451 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
      `    routes:`, `      - when: { path: { prefix: /api } }`,
      `        then: { proxy: api, waf: geo }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/api/private");
    expect(response.status).toBe(200);
    expect(upstream.hits().paths).toEqual(["/api/private"]);
  });
});

describe("WAF GeoIP provider", () => {
	it("fails closed when a valid MMDB has no record for the direct peer", async () => {
    const upstream = await startUpstream();
    servers.push(upstream);
    const address = await freeAddress();
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-mmdb-fixture-"));
    const databasePath = join(directory, "city.mmdb");
    await copyFile("fixtures/GeoIP2-City-Test.mmdb", databasePath);
    const configPath = await writeGatewayConfig([
      `dataProviders:`, `  geo: { type: mmdb, path: ${databasePath}, onError: deny }`,
      `upstreams:`, `  api:`, `    targets:`, `      - address: ${upstream.address}`,
      `wafPolicies:`, `  geo:`, `    rules:`,
      `      - when: { geo: { provider: geo, country: { exact: GB } }, path: { prefix: /api } }`,
      `        then: { deny: { status: 451 } }`,
      `listeners:`, `  web:`, `    type: http`, `    address: ${address}`,
      `    routes:`, `      - when: { path: { prefix: /api } }`,
      `        then: { proxy: api, waf: geo }`,
    ].join("\n"));
    const gateway = await startGateway(["--config", configPath, "serve", "--no-management"]);
    gateways.push(gateway);
    await waitReady(address);

    const response = await request(address, "/api/private");
	    expect(response.status).toBe(403);
	    expect(response.headers.get("content-type")).toContain("application/problem+json");
	    expect(JSON.parse(response.text)).toMatchObject({
	      status: 403,
	      code: "waf_provider_unavailable",
	    });
    expect(upstream.hits().paths).toEqual([]);
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
