import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
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

function request(address: string, path: string, token?: string): Promise<ResponseValue> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolve, reject) => {
    const requestValue = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path,
      method: "GET",
      rejectUnauthorized: false,
      headers: token === undefined ? undefined : { Authorization: `Bearer ${token}` },
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolve({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
    });
    requestValue.once("error", reject);
    requestValue.end();
  });
}

function requestJSON(address: string, path: string, token: string, body: unknown): Promise<ResponseValue> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolve, reject) => {
    const requestValue = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path,
      method: "POST",
      rejectUnauthorized: false,
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
      },
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolve({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
    });
    requestValue.once("error", reject);
    requestValue.end(JSON.stringify(body));
  });
}

async function waitForManagement(address: string, child: Awaited<ReturnType<typeof startGateway>>["process"]): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Gateway exited before Management API became ready (${child.exitCode}).`);
    try {
      const response = await request(address, "/healthz");
      if (response.status === 200) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error("Gateway Management API did not become ready.");
}

describe("production serve bootstrap and SQLite group reads", () => {
  it("starts management-only on an empty system group and serves authenticated SQLite-backed group reads", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-serve-bootstrap-groups-"));
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
      expect(bootstrap.stderr).toBe("");
      expect(token.length).toBeGreaterThan(0);

      gateway = await startGateway(["--config", config, "serve"]);
      await waitForManagement(address, gateway.process);

      const health = await request(address, "/healthz");
      expect(health.status).toBe(200);
      expect((await request(address, "/api/groups")).status).toBe(401);

      const status = await request(address, "/api/status", token);
      expect(status.status).toBe(200);
      expect(JSON.parse(status.body)).toMatchObject({
        dataPlaneReadiness: { state: "not-ready", reason: "system-release-required" },
      });

      const list = await request(address, "/api/groups", token);
      expect(list.status).toBe(200);
      expect(JSON.parse(list.body)).toMatchObject({
        items: [{ id: "system", kind: "system", active: true, currentRevision: null, previousRevision: null, state: "empty" }],
      });

      const system = await request(address, "/api/groups/system", token);
      expect(system.status).toBe(200);
      expect(JSON.parse(system.body)).toMatchObject({
        id: "system", kind: "system", active: true, currentRevision: null, previousRevision: null, state: "empty",
      });

      const created = await requestJSON(address, "/api/groups", token, {
        id: "portal",
        idempotencyKey: "create-portal-group-0001",
      });
      expect(created.status).toBe(201);
      expect(JSON.parse(created.body)).toMatchObject({
        id: "portal", kind: "application", active: true, currentRevision: null, previousRevision: null, state: "empty",
      });

      const invalid = await requestJSON(address, "/api/groups", token, {
        id: "Portal",
        idempotencyKey: "create-invalid-group-0001",
      });
      expect(invalid.status).toBe(400);
      expect(JSON.parse(invalid.body)).toMatchObject({ code: "invalid_request" });

      const duplicate = await requestJSON(address, "/api/groups", token, {
        id: "portal",
        idempotencyKey: "create-portal-group-0002",
      });
      expect(duplicate.status).toBe(409);
      expect(JSON.parse(duplicate.body)).toMatchObject({ code: "group_already_exists" });

      const listed = await request(address, "/api/groups", token);
      expect(JSON.parse(listed.body).items).toHaveLength(2);
      const databaseBytes = await readFile(database);
      expect(databaseBytes.byteLength).toBeGreaterThan(0);
      expect(databaseBytes.includes(Buffer.from(token))).toBe(false);
    } finally {
      if (gateway !== undefined) {
        await gateway.stop();
      }
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
