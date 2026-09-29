import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { request as httpsRequest } from "node:https";
import { describe, expect, it } from "vitest";
import { buildCoreTestBinary, startCore } from "../support/core.js";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);

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
  const certificate = join(directory, "management.crt");
  const privateKey = join(directory, "management.key");
  const pluginControlCertificate = join(directory, "plugin-control.crt");
  const pluginControlKey = join(directory, "plugin-control.key");
  const replicaClientCA = join(directory, "plugin-replica-ca.crt");
  const database = join(directory, "core.db");
  const config = join(directory, "core.yaml");
  await execFileAsync("openssl", [
    "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
    "-subj", "/CN=localhost",
    "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
    "-keyout", privateKey, "-out", certificate,
  ]);
  await execFileAsync("openssl", [
    "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
    "-subj", "/CN=plugin-control",
    "-keyout", pluginControlKey, "-out", pluginControlCertificate,
  ]);
  await execFileAsync("openssl", [
    "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
    "-subj", "/CN=plugin-replica-ca",
    "-keyout", join(directory, "plugin-replica-ca.key"), "-out", replicaClientCA,
  ]);
  await writeFile(config, [
    "state:",
    `  path: ${database}`,
    "management:",
    `  listen: ${address}`,
    "  tls:",
    `    certificate: file:${certificate}`,
    `    key: file:${privateKey}`,
    "pluginControl:",
    `  listen: ${pluginControlAddress}`,
    `  publicURL: https://${pluginControlAddress}`,
    "  tls:",
    `    certificate: file:${pluginControlCertificate}`,
    `    key: file:${pluginControlKey}`,
    `    replicaClientCA: file:${replicaClientCA}`,
    "",
  ].join("\n"), "utf8");
  const bootstrap = await execFileAsync(binary, ["--config", config, "access", "bootstrap"]);
  return { address, config, database, bootstrapToken: bootstrap.stdout.trim() };
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

describe("service-key metadata listing", () => {
  it("lists bootstrap and API-issued keys without verifier or raw-token material", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-service-key-list-"));
    let core: Awaited<ReturnType<typeof startCore>> | undefined;
    try {
      const prepared = await prepareCore(directory);
      core = await startCore(["--config", prepared.config, "serve"]);
      await waitForManagement(prepared.address, core.process);

      const created = await request(prepared.address, "POST", "/api/access/service-keys", prepared.bootstrapToken, { name: "controller" });
      expect(created.status).toBe(201);
      const issued = JSON.parse(created.body) as { id: string; token: string };
      expect(issued.id).not.toBe("");
      expect(issued.token).not.toBe("");

      const listed = await request(prepared.address, "GET", "/api/access/service-keys", prepared.bootstrapToken);
      expect(listed.status).toBe(200);
      const result = JSON.parse(listed.body) as {
        items: Array<Record<string, unknown>>;
        requestId: string;
      };
      expect(result.requestId).not.toBe("");
      expect(result.items).toHaveLength(2);
      expect(result.items.map((item) => item.name)).toEqual(expect.arrayContaining(["local-bootstrap", "controller"]));

      for (const item of result.items) {
        expect(Object.keys(item).sort()).toEqual(["createdAt", "expiresAt", "id", "name", "revokedAt", "role"].sort());
        expect(item).toMatchObject({ role: "platform-admin" });
        expect(item.createdAt).toEqual(expect.any(String));
        expect(item.expiresAt).toBeNull();
        expect(item.revokedAt).toBeNull();
        expect(JSON.stringify(item)).not.toContain(issued.token);
      }
      expect(result.items.find((item) => item.id === issued.id)).toMatchObject({
        name: "controller",
        role: "platform-admin",
      });
      expect(listed.body).not.toContain(issued.token);
      expect(listed.body).not.toContain("verifier");
    } finally {
      if (core !== undefined) await core.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
