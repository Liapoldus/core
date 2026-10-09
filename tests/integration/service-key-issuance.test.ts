import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { request as httpsRequest } from "node:https";
import { describe, expect, it } from "vitest";
import { buildCoreTestBinary, startCore } from "../support/core.js";
import { freeAddress } from "../support/http.js";
import { initializeCore } from "../support/initialize.js";

const execFileAsync = promisify(execFile);
const coreRoot = fileURLToPath(new URL("../..", import.meta.url));

interface ResponseValue {
  readonly status: number;
  readonly body: string;
}

function request(address: string, method: string, path: string, token?: string, body?: unknown): Promise<ResponseValue> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolve, reject) => {
    const requestValue = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path,
      method,
      rejectUnauthorized: false,
      headers: {
        ...(token === undefined ? {} : { Authorization: `Bearer ${token}` }),
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolve({ status: response.statusCode ?? 0, body: Buffer.concat(chunks).toString("utf8") }));
    });
    requestValue.once("error", reject);
    requestValue.end(body === undefined ? undefined : JSON.stringify(body));
  });
}

async function prepareCore(directory: string) {
  const binary = await buildCoreTestBinary();
  const address = await freeAddress();
  const pluginControlAddress = await freeAddress();
  const initialized = await initializeCore(binary, directory, address, pluginControlAddress);
  return { address, environment: initialized.environment, database: initialized.database, bootstrapToken: initialized.bootstrapToken };
}

async function waitForManagement(address: string, child: Awaited<ReturnType<typeof startCore>>["process"]): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Core exited before Management API became ready (${child.exitCode}).`);
    try {
      if ((await request(address, "GET", "/healthz")).status === 200) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error("Core Management API did not become ready.");
}

async function serviceKeyCount(database: string): Promise<number> {
  const result = await execFileAsync("go", ["run", "./tests/fixtures/sqlite-service-key-count", database], { cwd: coreRoot });
  return (JSON.parse(result.stdout) as { count: number }).count;
}

async function databaseBytes(database: string): Promise<Buffer> {
  const files = await Promise.all([database, `${database}-wal`, `${database}-shm`].map(async (path) => {
    try {
      return await readFile(path);
    } catch {
      return Buffer.alloc(0);
    }
  }));
  return Buffer.concat(files);
}

describe("one-time service-key issuance", () => {
  it("issues a platform-admin credential once, stores only its verifier and audits without secrets", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-service-key-"));
    let core: Awaited<ReturnType<typeof startCore>> | undefined;
    try {
      const prepared = await prepareCore(directory);
      core = await startCore(["serve"], prepared.environment);
      await waitForManagement(prepared.address, core.process);

      const created = await request(prepared.address, "POST", "/api/access/service-keys", prepared.bootstrapToken, { name: "controller" });
      expect(created.status).toBe(201);
      const credential = JSON.parse(created.body) as { id: string; name: string; role: string; token: string; requestId: string };
      expect(credential).toMatchObject({ name: "controller", role: "platform-admin" });
      expect(credential.id).not.toBe("");
      expect(credential.requestId).not.toBe("");
      expect(credential.token).toMatch(/^[A-Za-z0-9_-]+$/);

      expect((await request(prepared.address, "GET", "/api/status", credential.token)).status).toBe(200);
      expect(await serviceKeyCount(prepared.database)).toBe(2);
      expect((await databaseBytes(prepared.database)).includes(Buffer.from(credential.token))).toBe(false);

      for (const path of ["/api/status", "/api/plugins", "/api/audit"]) {
        const response = await request(prepared.address, "GET", path, credential.token);
        expect(response.status).toBe(200);
        expect(response.body).not.toContain(credential.token);
      }
      const audit = JSON.parse((await request(prepared.address, "GET", "/api/audit", credential.token)).body) as { items: unknown[] };
      expect(audit.items).toHaveLength(1);
    } finally {
      if (core !== undefined) await core.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);

  it("does not persist or return a credential when the atomic audit insert fails", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-service-key-audit-failure-"));
    let core: Awaited<ReturnType<typeof startCore>> | undefined;
    try {
      const prepared = await prepareCore(directory);
      await execFileAsync("go", ["run", "./tests/fixtures/sqlite-audit-trigger", prepared.database], { cwd: coreRoot });
      core = await startCore(["serve"], prepared.environment);
      await waitForManagement(prepared.address, core.process);

      const rejected = await request(prepared.address, "POST", "/api/access/service-keys", prepared.bootstrapToken, { name: "not-persisted" });
      expect(rejected.status).toBe(503);
      expect(JSON.parse(rejected.body)).not.toHaveProperty("token");
      expect(await serviceKeyCount(prepared.database)).toBe(1);
      expect((await request(prepared.address, "GET", "/api/audit", prepared.bootstrapToken)).body).not.toContain("not-persisted");
    } finally {
      if (core !== undefined) await core.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
