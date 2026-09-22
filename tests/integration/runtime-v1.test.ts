import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
afterEach(async () => Promise.all(gateways.splice(0).map((gateway) => gateway.stop())));

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

describe("HTTP runtime v1", () => {
  it("uses index.html for an extensionless HTML navigation when SPA is enabled", async () => {
    const address = await startSite("slug: web\nindex: index.html\nspa: true\n");
    const response = await request(address, "/dashboard", { headers: { accept: "text/html" } });
    expect(response.status).toBe(200);
    expect(response.text).toBe("shell");
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

  it("serves a satisfiable byte range and rejects an unsatisfiable range", async () => {
    const address = await startSite("slug: web\nindex: index.html\n");
    const partial = await request(address, "/assets/app.js", { headers: { range: "bytes=0-3" } });
    expect(partial.status).toBe(206);
    expect(partial.text).toBe("0123");
    expect(partial.headers.get("content-range")).toBe("bytes 0-3/10");
    const invalid = await request(address, "/assets/app.js", { headers: { range: "bytes=20-30" } });
    expect(invalid.status).toBe(416);
    expect(invalid.headers.get("content-range")).toBe("bytes */10");
  });

  it("emits configured static cache policy", async () => {
    const address = await startSite("slug: web\nindex: index.html\ncache:\n  static:\n    visibility: public\n    maxAge: 1h\n");
    const response = await request(address, "/assets/app.js");
    expect(response.headers.get("cache-control")).toBe("public, max-age=3600");
  });
});
