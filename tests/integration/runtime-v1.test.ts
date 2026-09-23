import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
afterEach(async () => Promise.all(gateways.splice(0).map((gateway) => gateway.stop())));

const gatewayVectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{
    id: string;
    input: { accept?: string; acceptLanguage?: string; defaultLocale?: string; etag?: string; ifNoneMatch?: string; locales?: string[]; path?: string; range?: string; size?: number };
    expected: { bodyBytes?: number; code?: string; file?: string; headers?: Record<string, string>; redirect?: boolean; releasePath?: string; status: number; varyAcceptLanguage?: boolean };
  }>;
};

function vector(id: string) {
  const match = gatewayVectors.vectors.find((candidate) => candidate.id === id);
  if (match === undefined) throw new Error(`missing gateway golden vector ${id}`);
  return match;
}

async function startSite(manifest: string): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), "liapoldus-runtime-v1-"));
  await mkdir(join(root, "assets"));
  await writeFile(join(root, "index.html"), "shell", "utf8");
  await writeFile(join(root, "assets", "app.js"), "0123456789", "utf8");
  await writeFile(join(root, "assets", "data.json"), "{}", "utf8");
  await writeFile(join(root, "site.yaml"), manifest, "utf8");
  const address = await freeAddress();
  const config = await writeGatewayConfig(["sites:", `  web: { source: { type: directory, root: ${root} } }`, "listeners:", "  public:", "    type: http", `    address: ${address}`, "    routes:", "      - when: { path: { prefix: / } }", "        then: { site: web }"].join("\n"));
  const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
  gateways.push(gateway);
  await waitReady(address);
  return address;
}

async function startLocaleSite(): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), "liapoldus-runtime-locale-"));
  for (const locale of ["ru", "en"]) {
    await mkdir(join(root, locale), { recursive: true });
    await writeFile(join(root, locale, "about"), `${locale} page`, "utf8");
  }
  await writeFile(join(root, "site.yaml"), "slug: web\nlocales: [ru, en]\ndefaultLocale: ru\n", "utf8");
  const address = await freeAddress();
  const config = await writeGatewayConfig(["sites:", `  web: { source: { type: directory, root: ${root} } }`, "listeners:", "  public:", "    type: http", `    address: ${address}`, "    routes:", "      - when: { path: { prefix: / } }", "        then: { site: web }"].join("\n"));
  const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
  gateways.push(gateway);
  await waitReady(address);
  return address;
}

describe("HTTP runtime v1", () => {
  it("uses index.html for an extensionless HTML navigation when SPA is enabled", async () => {
    const address = await startSite("slug: web\nindex: index.html\nspa: true\n");
    const expected = vector("spa-fallback");
    const response = await request(address, expected.input.path!, { headers: { accept: expected.input.accept! } });
    expect(response.status).toBe(expected.expected.status);
    expect(response.text).toBe("shell");
  });

  it("returns the contracted problem for a missing SPA asset", async () => {
    const expected = vector("spa-asset-miss");
    const address = await startSite("slug: web\nindex: index.html\nspa: true\n");
    const response = await request(address, expected.input.path!, { headers: { accept: expected.input.accept! } });
    expect(response.status).toBe(expected.expected.status);
    expect(response.headers.get("content-type")).toContain("application/problem+json");
    expect(JSON.parse(response.text).code).toBe(expected.expected.code);
  });

  it("preserves an explicit declared locale prefix", async () => {
    const expected = vector("locale-prefixed");
    const address = await startLocaleSite();
    const response = await request(address, expected.input.path!);
    expect(response.status).toBe(expected.expected.status);
    expect(response.text).toBe("ru page");
  });

  it("maps unprefixed paths to the default locale without redirect or language negotiation", async () => {
    const expected = vector("locale-unprefixed");
    const address = await startLocaleSite();
    const response = await request(address, expected.input.path!, { headers: { "accept-language": expected.input.acceptLanguage! } });
    expect(response.status).toBe(200);
    expect(response.text).toBe("ru page");
    expect(Boolean(response.headers.get("location"))).toBe(expected.expected.redirect);
    expect(response.headers.get("vary")?.toLowerCase().includes("accept-language") ?? false).toBe(expected.expected.varyAcceptLanguage);
  });

  it("only allows GET and HEAD on health endpoints", async () => {
    const address = await startSite("slug: web\nindex: index.html\n");
    const response = await fetch(`http://${address}/healthz`, { method: "POST" });
    expect(response.status).toBe(404);
  });

  it("compresses eligible responses when gzip is accepted", async () => {
    const address = await startSite("slug: web\nindex: index.html\n");
    const response = await fetch(`http://${address}/assets/data.json`, { headers: { "accept-encoding": "gzip" } });
    expect(response.status).toBe(200);
    expect(response.headers.get("content-encoding")).toBe("gzip");
    expect(response.headers.get("vary")).toContain("Accept-Encoding");
  });

  it("emits MIME, validators and conditional 304 for static files", async () => {
    const address = await startSite("slug: web\nindex: index.html\n");
    const first = await request(address, "/assets/data.json");
    expect(first.status).toBe(200);
    expect(first.headers.get("content-type")).toContain("application/json");
    const etag = first.headers.get("etag");
    expect(etag).toBeTruthy();
    const second = await request(address, "/assets/data.json", { headers: { "if-none-match": etag! } });
    expect(second.status).toBe(304);
    expect(second.text).toBe("");
  });

  it("satisfies the static-etag-not-modified golden vector", async () => {
    const expected = vector("static-etag-not-modified");
    const address = await startSite("slug: web\nindex: index.html\n");
    const first = await request(address, "/assets/app.js");
    const etag = first.headers.get("etag");
    expect(etag).toBeTruthy();
    expect(expected.input.ifNoneMatch).toBe(expected.input.etag);
    const notModified = await request(address, "/assets/app.js", { headers: { "if-none-match": etag! } });
    expect(notModified.status).toBe(expected.expected.status);
    expect(notModified.text).toBe("");
  });

  it("serves a satisfiable byte range and rejects an unsatisfiable range", async () => {
    const address = await startSite("slug: web\nindex: index.html\n");
    const satisfiable = vector("static-range-single");
    const partial = await request(address, "/assets/app.js", { headers: { range: satisfiable.input.range! } });
    expect(partial.status).toBe(satisfiable.expected.status);
    expect(Buffer.byteLength(partial.text)).toBe(satisfiable.expected.bodyBytes);
    expect(partial.headers.get("content-range")).toBe(satisfiable.expected.headers?.["Content-Range"]);
    const unsatisfiable = vector("static-range-unsatisfiable");
    const invalid = await request(address, "/assets/app.js", { headers: { range: unsatisfiable.input.range! } });
    expect(invalid.status).toBe(unsatisfiable.expected.status);
    expect(invalid.headers.get("content-range")).toBe(unsatisfiable.expected.headers?.["Content-Range"]);
  });

  it("emits configured static cache policy", async () => {
    const address = await startSite("slug: web\nindex: index.html\ncache:\n  static:\n    visibility: public\n    maxAge: 1h\n");
    const response = await request(address, "/assets/app.js");
    expect(response.headers.get("cache-control")).toBe("public, max-age=3600");
  });
});
