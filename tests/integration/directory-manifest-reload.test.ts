import { afterEach, describe, expect, it } from "vitest";
import type { ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, writeFile } from "node:fs/promises";
import { readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { startGateway } from "../support/gateway.js";
import { freeAddress, request, waitReady, writeGatewayConfig } from "../support/http.js";

const token = "directory-manifest-reload-test-token";
const environment = { LIAPOLDUS_TEST_MANAGEMENT_TOKEN: token };
const gateways: Array<{ process: ChildProcess; stop(): Promise<void> }> = [];
const vectors = JSON.parse(readFileSync(resolve(import.meta.dirname, "../../contracts/v1/golden-vectors.json"), "utf8")) as {
  vectors: Array<{ id: string; input: { directorySiteYamlChanged: boolean; reload: boolean }; expected: { manifestReparsed: boolean; activeSnapshotChanged: boolean } }>;
};
const vector = vectors.vectors.find(({ id }) => id === "directory-manifest-reload");
if (!vector) throw new Error("directory-manifest-reload vector is missing");

afterEach(async () => {
  for (const gateway of gateways.splice(0)) await gateway.stop();
});

describe("directory site manifest reload", () => {
  it("reparses a changed site.yaml into the active runtime snapshot", async () => {
    expect(vector.input).toEqual({ directorySiteYamlChanged: true, reload: true });
    const root = await mkdtemp(join(tmpdir(), "liapoldus-directory-manifest-reload-"));
    await mkdir(root, { recursive: true });
    await writeFile(join(root, "index.html"), "directory manifest fixture", "utf8");
    const manifestPath = join(root, "site.yaml");
    await writeFile(manifestPath, [
      "slug: docs",
      "headers:",
      "  response:",
      "    set:",
      "      x-site-generation: one",
    ].join("\n"), "utf8");

    const webAddress = await freeAddress();
    const managementAddress = await freeAddress();
    const configPath = await writeGatewayConfig([
      "sites:",
      "  docs:",
      "    source:",
      "      type: directory",
      `      root: ${root}`,
      "listeners:",
      "  web:",
      "    type: http",
      `    address: ${webAddress}`,
      "    routes:",
      "      - when: { path: { prefix: / } }",
      "        then: { site: docs }",
      "management:",
      `  listener: { address: ${managementAddress} }`,
      "  staticToken: env:LIAPOLDUS_TEST_MANAGEMENT_TOKEN",
    ].join("\n"));
    const gateway = await startGateway(["--config", configPath, "serve"], environment);
    gateways.push(gateway);
    await waitReady(webAddress);
    await waitReady(managementAddress);

    const auth = { Authorization: `Bearer ${token}` };
    const beforeResponse = await request(managementAddress, "/api/status", { headers: auth });
    const before = JSON.parse(beforeResponse.text) as { revision: string };
    const beforePage = await request(webAddress, "/");
    expect(beforePage.status).toBe(200);
    expect(beforePage.headers.get("x-site-generation")).toBe("one");

    await writeFile(manifestPath, [
      "slug: docs",
      "headers:",
      "  response:",
      "    set:",
      "      x-site-generation: two",
    ].join("\n"), "utf8");
    const beforeReloadPage = await request(webAddress, "/");
    expect(beforeReloadPage.headers.get("x-site-generation")).toBe("one");
    const reload = await request(managementAddress, "/api/reload", {
      method: "POST",
      headers: { ...auth, "If-Match": before.revision },
    });
    const afterPage = await request(webAddress, "/");

    expect(reload.status).toBe(202);
    expect(vector.expected.manifestReparsed).toBe(true);
    expect(vector.expected.activeSnapshotChanged).toBe(true);
    expect(afterPage.status).toBe(200);
    expect(afterPage.headers.get("x-site-generation")).toBe("two");
  });
});
