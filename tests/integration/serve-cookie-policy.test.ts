import { execFile } from "node:child_process";
import { access, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { request as httpsRequest } from "node:https";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary, startGatewayWithOutput } from "../support/gateway.js";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

interface ResponseValue {
  readonly status: number;
  readonly body: string;
  readonly setCookie: string;
}

function request(address: string, path: string, token: string, method = "GET", cookie?: string): Promise<ResponseValue> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return new Promise((resolve, reject) => {
    const requestValue = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path,
      method,
      rejectUnauthorized: false,
      headers: {
        Authorization: `Bearer ${token}`,
        ...(cookie === undefined ? {} : { Cookie: cookie }),
      },
    }, (response) => {
      const chunks: Buffer[] = [];
      response.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      response.once("end", () => resolve({
        status: response.statusCode ?? 0,
        body: Buffer.concat(chunks).toString("utf8"),
        setCookie: response.headers["set-cookie"]?.join("\n") ?? "",
      }));
    });
    requestValue.once("error", reject);
    requestValue.end();
  });
}

async function waitForManagement(address: string, child: Awaited<ReturnType<typeof startGateway>>["process"]): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (child.exitCode !== null) throw new Error(`Gateway exited before Management API became ready (${child.exitCode}).`);
    try {
      if ((await request(address, "/healthz", "unused")).status === 200) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }
  throw new Error("Gateway Management API did not become ready.");
}

async function buildFixture(name: string, output: string): Promise<void> {
  await execFileAsync("go", ["build", "-o", output, `./tests/fixtures/${name}`], { cwd: coreRoot });
}

describe("production serve cookie policy restoration", () => {
  it("restores SQLite policy into embedded Caddy and applies typed cookie actions atomically", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-serve-cookie-policy-"));
    const binary = await buildGatewayTestBinary();
    const managementAddress = await freeAddress();
    const publicAddress = await freeAddress();
    const certificate = join(directory, "management.crt");
    const privateKey = join(directory, "management.key");
    const database = join(directory, "gateway.db");
    const artifacts = join(directory, "artifacts");
    const config = join(directory, "gateway.yaml");
    const pluginBinary = join(directory, "cookie-plugin");
    let gateway: Awaited<ReturnType<typeof startGatewayWithOutput>> | undefined;

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
        `  listen: ${managementAddress}`,
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
      await buildFixture("serve-cookie-policy-plugin", pluginBinary);
      const seeded = await execFileAsync("go", [
        "run", "./tests/fixtures/serve-cookie-policy-seed", database, artifacts, pluginBinary, publicAddress,
      ], { cwd: coreRoot });
      expect(JSON.parse(seeded.stdout)).toMatchObject({ activeRevisionSeeded: true, cookiePolicySeeded: true });

      gateway = await startGatewayWithOutput(["--config", config, "serve"]);
      await waitForManagement(managementAddress, gateway.process);
      const readiness = await request(managementAddress, "/api/status", token);
      expect(JSON.parse(readiness.body)).toMatchObject({ dataPlaneReadiness: { state: "ready" } });

      const accepted = await fetch(`http://${publicAddress}/accepted`, {
        headers: { cookie: "session=allowed; theme=blocked" },
      });
      const acceptedBody = await accepted.text();
      let pluginCalled = false;
      try {
        await access(`${pluginBinary}.called`);
        pluginCalled = true;
      } catch {
        pluginCalled = false;
      }
      expect(pluginCalled, gateway.stderr).toBe(true);
      expect(accepted.status, `${acceptedBody}\n${gateway.stderr}`).toBe(200);
      expect(JSON.parse(acceptedBody)).toEqual([{ name: "session", value: "allowed" }]);
      const setCookie = accepted.headers.get("set-cookie") ?? "";
      const serializedCookies = setCookie.split(/, (?=[^;,]+=)/);
      const ordinaryCookie = serializedCookies.find((value) => value.startsWith("theme=")) ?? "";
      const httpOnlyCookie = serializedCookies.find((value) => value.startsWith("liap-session=")) ?? "";
      expect(ordinaryCookie).toContain("theme=synthetic-ordinary-value");
      expect(ordinaryCookie).not.toMatch(/;\s*HttpOnly/i);
      expect(httpOnlyCookie).toContain("liap-session=synthetic-httponly-value");
      expect(httpOnlyCookie).toMatch(/;\s*HttpOnly/i);

      const rejected = await fetch(`http://${publicAddress}/rejected`);
      expect(rejected.status).toBe(502);
      expect(rejected.headers.get("set-cookie")).toBeNull();
    } finally {
      if (gateway !== undefined) await gateway.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
