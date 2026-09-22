import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

interface GatewayHandle {
  readonly process: ChildProcess;
  stop(): Promise<void>;
}

const gateways: GatewayHandle[] = [];

afterEach(async () => {
  await Promise.all(gateways.splice(0).map((gateway) => gateway.stop()));
});

async function startManifestSite(manifest: string): Promise<string> {
  const root = await mkdtemp(join(tmpdir(), "liapoldus-site-manifest-"));
  await mkdir(join(root, "assets"));
  await writeFile(join(root, "index.html"), "site index", "utf8");
  await writeFile(join(root, "assets", "logo.txt"), "asset", "utf8");
  await writeFile(join(root, "site.yaml"), manifest, "utf8");
  const address = await freeAddress();
  const config = await writeGatewayConfig(
    [
      "sites:",
      `  web: { source: { type: directory, root: ${root} } }`,
      "listeners:",
      "  public:",
      "    type: http",
      `    address: ${address}`,
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { site: web }",
    ].join("\n"),
  );
  const gateway = await startGateway(["--config", config, "serve", "--no-management"]);
  gateways.push(gateway);
  await waitReady(address);
  return address;
}

describe("site.yaml runtime behavior", () => {
  it("applies manifest redirects before attempting static file resolution", async () => {
    const address = await startManifestSite(
      ["slug: web", "redirects:", "  - from: /legacy", "    to: /new", "    status: 301"].join("\n"),
    );

    const response = await request(address, "/legacy", { redirect: "manual" });

    expect(response.status).toBe(301);
    expect(response.headers.get("location")).toBe("/new");
  });

  it("adds site response headers to files served by the static terminal", async () => {
    const address = await startManifestSite(
      [
        "slug: web",
        "headers:",
        "  response:",
        "    set:",
        "      x-site-policy: enabled",
      ].join("\n"),
    );

    const response = await request(address, "/assets/logo.txt");

    expect(response.status).toBe(200);
    expect(response.headers.get("x-site-policy")).toBe("enabled");
  });
});
