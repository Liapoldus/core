import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { request as httpsRequest } from "node:https";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary, startGateway } from "../support/gateway.js";
import { freeAddress } from "../support/http.js";

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

async function waitForManagement(address: string, child: Awaited<ReturnType<typeof startGateway>>["process"]): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Gateway exited before Management API became ready (${child.exitCode}).`);
    try {
      const response = await request(address, "/healthz", "unused");
      if (response.status === 200) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error("Gateway Management API did not become ready.");
}

describe("serve plugin runtime composition", () => {
  it("loads SQLite plugin instances and exposes them only through the ready management runtime", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-serve-plugin-runtime-"));
    const binary = await buildGatewayTestBinary();
    const address = await freeAddress();
    const certificate = join(directory, "management.crt");
    const privateKey = join(directory, "management.key");
    const database = join(directory, "gateway.db");
    const artifacts = join(directory, "artifacts");
    const config = join(directory, "gateway.yaml");
    let gateway: Awaited<ReturnType<typeof startGateway>> | undefined;

    try {
      await execFileAsync("openssl", [
        "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
        "-subj", "/CN=localhost",
        "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
        "-keyout", privateKey, "-out", certificate,
      ]);
      await writeFile(config, [
        "state:",
        `  path: ${database}`,
        "artifacts:",
        `  path: ${artifacts}`,
        "management:",
        `  listen: ${address}`,
        "  tls:",
        `    certificate: file:${certificate}`,
        `    key: file:${privateKey}`,
        "caddy:",
        "  variant: embedded",
        "",
      ].join("\n"), "utf8");

      const bootstrap = await execFileAsync(binary, ["--config", config, "access", "bootstrap"]);
      const token = bootstrap.stdout.trim();
      expect(token.length).toBeGreaterThan(0);

      const seeded = await execFileAsync("go", ["run", "./tests/fixtures/serve-plugin-runtime", database], {
        cwd: join(import.meta.dirname, "../.."),
      });
      const seedReport = JSON.parse(seeded.stdout) as { columns: string[]; seeded: boolean };
      expect(seedReport.seeded).toBe(true);
      expect(seedReport.columns).toEqual(expect.arrayContaining([
        "id", "mode", "endpoint", "settings_json", "manifest_json", "state", "revision",
      ]));

      gateway = await startGateway(["--config", config, "serve"]);
      await waitForManagement(address, gateway.process);

      const status = await request(address, "/api/status", token);
      expect(status.status).toBe(200);
      expect(JSON.parse(status.body)).toMatchObject({
        dataPlaneReadiness: { state: "not-ready", reason: "system-release-required" },
      });

      const pluginList = await request(address, "/api/plugins", token);
      expect(pluginList.status).toBe(200);
      expect(JSON.parse(pluginList.body).items).toEqual(expect.arrayContaining([
        expect.objectContaining({ id: "serve-fixture", mode: "local" }),
      ]));

      const surfaces = await request(address, "/api/plugins/admin-surfaces", token);
      expect(surfaces.status).toBe(200);
      expect(JSON.parse(surfaces.body).items).not.toEqual(expect.arrayContaining([
        expect.objectContaining({ plugin: "serve-fixture" }),
      ]));

      const restart = await request(address, "/api/plugins/serve-fixture/restart", token, "POST");
      expect(restart.status).not.toBe(202);
    } finally {
      if (gateway !== undefined) await gateway.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
