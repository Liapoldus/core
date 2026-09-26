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
const workspace = join(import.meta.dirname, "../../..");
const core = join(workspace, "core");
const plugins = join(workspace, "plugins");

async function buildPlugin(name: string, output: string): Promise<void> {
  await execFileAsync("go", ["build", "-o", output, `./cmd/${name}`], {
    cwd: join(plugins, name),
  });
}

async function request(address: string, path: string, method = "GET", body?: string): Promise<Response> {
  const port = Number(address.slice(address.lastIndexOf(":") + 1));
  return await new Promise((resolve, reject) => {
    const outgoing = httpsRequest({
      hostname: "127.0.0.1",
      port,
      path,
      method,
      rejectUnauthorized: false,
      headers: body === undefined ? undefined : { "content-type": "application/json", "content-length": Buffer.byteLength(body) },
    }, (incoming) => {
      const chunks: Buffer[] = [];
      incoming.on("data", (chunk: Buffer) => chunks.push(Buffer.from(chunk)));
      incoming.once("end", () => resolve(new Response(Buffer.concat(chunks), { status: incoming.statusCode ?? 0, headers: incoming.headers as HeadersInit })));
    });
    outgoing.once("error", reject);
    if (body !== undefined) outgoing.write(body);
    outgoing.end();
  });
}

async function waitForPublic(addresses: readonly string[], gateway: Awaited<ReturnType<typeof startGateway>>["process"]): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (gateway.exitCode !== null) throw new Error(`Gateway exited before public Caddy readiness (${gateway.exitCode}).`);
    try {
      const statuses = await Promise.all(addresses.map(async (address) => (await request(address, "/healthz")).status));
      if (statuses.every((status) => status === 404)) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("Embedded Caddy did not become ready on the plugin listeners.");
}

describe("serve real locally supervised plugins", () => {
  it("bootstraps and dispatches to captcha, forms-db, and identity child binaries", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-real-plugin-smoke-"));
    const gatewayBinary = await buildGatewayTestBinary();
    const captchaBinary = join(directory, "captcha-plugin");
    const formsBinary = join(directory, "forms-db-plugin");
    const identityBinary = join(directory, "identity-plugin");
    const managementAddress = await freeAddress();
    const captchaAddress = await freeAddress();
    const formsAddress = await freeAddress();
    const identityAddress = await freeAddress();
    const certificate = join(directory, "management.crt");
    const privateKey = join(directory, "management.key");
    const database = join(directory, "gateway.db");
    const artifacts = join(directory, "artifacts");
    const config = join(directory, "gateway.yaml");
    let gateway: Awaited<ReturnType<typeof startGateway>> | undefined;

    try {
      await Promise.all([
        buildPlugin("captcha", captchaBinary),
        buildPlugin("forms-db", formsBinary),
        buildPlugin("identity", identityBinary),
      ]);
      await execFileAsync("openssl", [
        "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
        "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
        "-keyout", privateKey, "-out", certificate,
      ]);
      await writeFile(config, [
        "state:", `  path: ${database}`,
        "artifacts:", `  path: ${artifacts}`,
        "management:", `  listen: ${managementAddress}`,
        "  tls:", `    certificate: file:${certificate}`, `    key: file:${privateKey}`,
        "caddy:", "  variant: embedded", "",
      ].join("\n"), "utf8");
      const bootstrap = await execFileAsync(gatewayBinary, ["--config", config, "access", "bootstrap"]);
      expect(bootstrap.stdout.trim()).not.toHaveLength(0);
      await execFileAsync("go", [
        "run", "./tests/fixtures/serve-local-plugin-products", database, artifacts,
        captchaAddress, formsAddress, identityAddress, captchaBinary, formsBinary, identityBinary,
      ], { cwd: core });

      gateway = await startGateway(["--config", config, "serve"]);
      await waitForPublic([captchaAddress, formsAddress, identityAddress], gateway.process);

      const captcha = await request(captchaAddress, "/verify", "POST", JSON.stringify({ token: "fixture-valid" }));
      expect(captcha.status).toBe(200);
      await expect(captcha.json()).resolves.toMatchObject({ valid: true });

      const submission = await request(formsAddress, "/submit", "POST", JSON.stringify({
        site: "smoke",
        schemaName: "contact",
        data: { email: "smoke@example.invalid" },
      }));
      expect(submission.status).toBe(200);
      await expect(submission.json()).resolves.toMatchObject({ data: { email: "smoke@example.invalid" } });

      const identity = await request(identityAddress, "/jwks");
      expect(identity.status).toBe(200);
      await expect(identity.json()).resolves.toEqual({ keys: [] });
    } finally {
      if (gateway !== undefined) await gateway.stop();
      await rm(directory, { recursive: true, force: true });
    }
  }, 240_000);
});
