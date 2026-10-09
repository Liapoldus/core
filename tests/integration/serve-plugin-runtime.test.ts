import { execFile } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { request as httpsRequest } from "node:https";
import { describe, expect, it } from "vitest";
import { buildCoreTestBinary, startCore } from "../support/core.js";
import { freeAddress } from "../support/http.js";
import { initializeCore } from "../support/initialize.js";

const execFileAsync = promisify(execFile);

interface ResponseValue {
  readonly status: number;
  readonly body: string;
}

function request(address: string, path: string, token: string, method = "GET"): Promise<ResponseValue> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolve, reject) => {
    const requestValue = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path,
      method,
      rejectUnauthorized: false,
      headers: { Authorization: `Bearer ${token}` },
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolve({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
    });
    requestValue.once("error", reject);
    requestValue.end();
  });
}

async function waitForManagement(address: string, child: Awaited<ReturnType<typeof startCore>>["process"]): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Core exited before Management API became ready (${child.exitCode}).`);
    try {
      const response = await request(address, "/healthz", "unused");
      if (response.status === 200) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error("Core Management API did not become ready.");
}

describe("serve plugin composition", () => {
	it("keeps Management available while a desired instance has no live registration lease", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-serve-plugin-runtime-"));
    const binary = await buildCoreTestBinary();
    const address = await freeAddress();
    const controlAddress = await freeAddress();
    let core: Awaited<ReturnType<typeof startCore>> | undefined;

    try {
      const initialized = await initializeCore(binary, directory, address, controlAddress);
      const { database, environment } = initialized;
      const token = initialized.bootstrapToken;
      expect(token.length).toBeGreaterThan(0);

      const seeded = await execFileAsync("go", ["run", "./tests/fixtures/serve-plugin-runtime", database], {
        cwd: join(import.meta.dirname, "../.."),
      });
      const seedReport = JSON.parse(seeded.stdout) as { columns: string[]; configurationColumns: string[]; seeded: boolean };
      expect(seedReport.seeded).toBe(true);
      expect(seedReport.columns).toEqual(expect.arrayContaining(["id", "manifest_json", "state"]));
      expect(seedReport.columns).not.toContain("mode");
      expect(seedReport.columns).not.toContain("endpoint");
      expect(seedReport.columns).not.toContain("settings_json");
      expect(seedReport.configurationColumns).toEqual(expect.arrayContaining(["instance_id", "generation", "slot", "raw_json", "sha256"]));

      core = await startCore(["serve"], environment);
      await waitForManagement(address, core.process);

      const status = await request(address, "/api/status", token);
      expect(status.status).toBe(200);
      const statusBody = JSON.parse(status.body) as { drift: boolean; dataPlaneReadiness: { state: string } };
		expect(statusBody.drift).toBe(true);
		expect(statusBody.dataPlaneReadiness.state).toBe("not-ready");

      // Core owns generic instance metadata only. Manifest documents and
      // registered endpoints are plugin-owned and must not be surfaced.
      const pluginList = await request(address, "/api/plugins", token);
      expect(pluginList.status).toBe(200);
      const pluginListBody = JSON.parse(pluginList.body) as { items: Array<Record<string, unknown>> };
      expect(pluginListBody.items).toEqual([
        { id: "serve-fixture", state: "configured", revision: 1 },
      ]);
      expect(pluginList.body).not.toContain("127.0.0.1:45678");
      expect(pluginList.body).not.toContain("fixture-private-value");
      expect(pluginList.body).not.toContain("settings_json");
      expect(pluginList.body).not.toContain("capabilit");
      expect(pluginList.body).not.toContain("protocolVersion");

      const pluginDetail = await request(address, "/api/plugins/serve-fixture", token);
      expect(pluginDetail.status).toBe(200);
      expect(JSON.parse(pluginDetail.body)).toEqual({
        id: "serve-fixture", state: "configured", revision: 1,
      });
      expect(pluginDetail.body).not.toContain("fixture-private-value");
      expect(pluginDetail.body).not.toContain("127.0.0.1:45678");

      const unknownPlugin = await request(address, "/api/plugins/unknown-fixture", token);
      expect(unknownPlugin.status).toBe(404);
      expect(JSON.parse(unknownPlugin.body).status).toBe(404);

      const surfaces = await request(address, "/api/plugins/admin-surfaces", token);
      expect(surfaces.status).toBe(503);
      expect(JSON.parse(surfaces.body).code).toBe("plugin_unavailable");

      // v1 Core owns no process lifecycle: install, restart and release are not
      // Management API operations.
      for (const path of ["/api/plugins/serve-fixture/restart", "/api/plugins/serve-fixture/install"]) {
        const response = await request(address, path, token, "POST");
        expect([404, 405], path).toContain(response.status);
      }
    } finally {
      if (core !== undefined) await core.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
