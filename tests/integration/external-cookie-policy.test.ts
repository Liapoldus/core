import { execFile } from "node:child_process";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { request as httpsRequest } from "node:https";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary, startGatewayWithOutput } from "../support/gateway.js";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");
const externalBuildID = "liapoldus-external-caddy-fixture";

interface ManagementResponse {
  readonly status: number;
  readonly body: string;
  readonly headers: Record<string, string | string[] | undefined>;
}

function managementRequest(
  address: string,
  path: string,
  token: string,
  method = "GET",
  headers: Record<string, string> = {},
  body?: string,
): Promise<ManagementResponse> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolve, reject) => {
    const requestValue = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path,
      method,
      rejectUnauthorized: false,
      headers: { Authorization: `Bearer ${token}`, ...headers },
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolve({
        status: response.statusCode ?? 0,
        body: Buffer.concat(chunks).toString("utf8"),
        headers: response.headers,
      }));
    });
    requestValue.once("error", reject);
    requestValue.end(body);
  });
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

async function buildFixture(name: string, output: string): Promise<void> {
  await execFileAsync("go", ["build", "-o", output, `./tests/fixtures/${name}`], { cwd: coreRoot });
}

async function waitForManagement(address: string, child: Awaited<ReturnType<typeof startGatewayWithOutput>>["process"]): Promise<void> {
  let lastError = "no response";
  for (let attempt = 0; attempt < 600; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Gateway exited before Management API became ready (${child.exitCode}).`);
    try {
      if ((await managementRequest(address, "/healthz", "unused")).status === 200) return;
    } catch (error) {
      lastError = String(error);
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error(`Gateway Management API did not become ready: ${lastError}`);
}

describe("external Caddy cookie policy activation", () => {
  it("activates a cookie-policy PUT in the supervised external Caddy snapshot before returning", async () => {
    const directory = await mkdtemp(join(tmpdir(), "lc-"));
    const gatewayBinary = await buildGatewayTestBinary();
    const managementAddress = await freeAddress();
    const publicAddress = await freeAddress();
    const certificate = join(directory, "management.crt");
    const privateKey = join(directory, "management.key");
    const database = join(directory, "gateway.db");
    const artifacts = join(directory, "artifacts");
    const config = join(directory, "gateway.yaml");
    const pluginBinary = join(directory, "cookie-plugin");
    const caddyBinary = join(directory, "liapoldus-caddy");
    const externalWrapper = join(directory, "liapoldus-caddy-wrapper");
    const externalLog = join(directory, "external-caddy.log");
    let gateway: Awaited<ReturnType<typeof startGatewayWithOutput>> | undefined;

    try {
      await Promise.all([
        buildFixture("serve-cookie-policy-plugin", pluginBinary),
        buildFixture("external-caddy-custom", caddyBinary),
      ]);
      await writeFile(externalWrapper, [
        "#!/bin/sh",
        `if [ "$1" = version ]; then echo ${externalBuildID}; exit 0; fi`,
        `exec ${shellQuote(caddyBinary)} "$@" 2>>${shellQuote(externalLog)}`,
        "",
      ].join("\n"), { mode: 0o700 });
      await chmod(externalWrapper, 0o700);
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
        `  listen: ${managementAddress}`,
        "  tls:",
        `    certificate: file:${certificate}`,
        `    key: file:${privateKey}`,
        "caddy:",
        "  variant: external",
        `  binary: ${externalWrapper}`,
        `  expectedBuildID: ${externalBuildID}`,
        "",
      ].join("\n"), "utf8");

      const bootstrap = await execFileAsync(gatewayBinary, ["--config", config, "access", "bootstrap"]);
      const token = bootstrap.stdout.trim();
      expect(token.length).toBeGreaterThan(0);
      const seeded = await execFileAsync("go", [
        "run", "./tests/fixtures/serve-cookie-policy-seed", database, artifacts, pluginBinary, publicAddress,
      ], { cwd: coreRoot });
      expect(JSON.parse(seeded.stdout)).toMatchObject({ activeRevisionSeeded: true, cookiePolicySeeded: true });

      gateway = await startGatewayWithOutput(["--config", config, "serve"]);
      await waitForManagement(managementAddress, gateway.process);
      const status = await managementRequest(managementAddress, "/api/status", token);
      expect(JSON.parse(status.body), `${status.body}\n${gateway.stderr}`).toMatchObject({ dataPlaneReadiness: { state: "ready" } });
      const before = await fetch(`http://${publicAddress}/accepted`, {
        headers: { cookie: "session=allowed; theme=blocked" },
      });
      expect(before.status).toBe(200);
      expect(await before.json()).toEqual([{ name: "session", value: "allowed" }]);

      const policyPath = "/api/plugins/cookie-fixture/cookie-policies/test.cookie-boundary";
      const currentPolicy = await managementRequest(managementAddress, policyPath, token);
      expect(currentPolicy.status).toBe(200);
      const etag = currentPolicy.headers.etag;
      expect(typeof etag).toBe("string");
      const updated = await managementRequest(
        managementAddress,
        policyPath,
        token,
        "PUT",
        { "If-Match": etag as string, "Content-Type": "application/json" },
        JSON.stringify({ allowedNames: ["theme"] }),
      );

      expect(updated.status, `${updated.body}\n${gateway.stderr}`).toBe(200);
      expect(updated.headers.etag).not.toBe(etag);
      expect(JSON.parse(updated.body)).toMatchObject({ revision: 8, allowedNames: ["theme"] });

      const after = await fetch(`http://${publicAddress}/accepted`, {
        headers: { cookie: "session=blocked; theme=allowed" },
      });
      expect(after.status).toBe(200);
      expect(await after.json()).toEqual([{ name: "theme", value: "allowed" }]);
    } catch (error) {
      const externalDiagnostics = await readFile(externalLog, "utf8").catch(() => "");
      throw new Error(`Gateway exit: ${gateway?.process.exitCode ?? "running"}\nGateway stdout: ${gateway?.stdout ?? ""}\nGateway stderr: ${gateway?.stderr ?? ""}\nExternal Caddy stderr: ${externalDiagnostics}\n${String(error)}`);
    } finally {
      if (gateway !== undefined) await gateway.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 240_000);
});
