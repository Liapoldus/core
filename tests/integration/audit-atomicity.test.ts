import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { request as httpsRequest } from "node:https";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary, startGateway } from "../support/gateway.js";
import { freeAddress } from "../support/http.js";

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

async function waitForManagement(address: string, child: Awaited<ReturnType<typeof startGateway>>["process"]): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Gateway exited before Management API became ready (${child.exitCode}).`);
    try {
      if ((await request(address, "GET", "/healthz")).status === 200) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error("Gateway Management API did not become ready.");
}

describe("group creation and audit atomicity", () => {
  it("does not commit a group or return success when its audit row cannot be persisted", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-audit-atomicity-"));
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

      await execFileAsync("go", ["run", "./tests/fixtures/sqlite-audit-trigger", database], { cwd: coreRoot });

      gateway = await startGateway(["--config", config, "serve"]);
      await waitForManagement(address, gateway.process);

      const created = await request(address, "POST", "/api/groups", token, {
        id: "unrecorded-group",
        idempotencyKey: "create-unrecorded-group-0001",
      });
      expect(created.status).toBe(503);
      expect(JSON.parse(created.body)).toMatchObject({ code: "audit_unavailable" });

      const groups = await request(address, "GET", "/api/groups", token);
      expect(groups.status).toBe(200);
      expect(JSON.parse(groups.body).items.map((group: { id: string }) => group.id)).not.toContain("unrecorded-group");

      const audit = await request(address, "GET", "/api/audit", token);
      expect(audit.status).toBe(200);
      expect(JSON.parse(audit.body).items).toEqual([]);
    } finally {
      if (gateway !== undefined) await gateway.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
