import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { buildGatewayTestBinary, startGatewayWithOutput } from "../support/gateway.js";
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
  return fetch(`http://${address}${path}`, {
    method,
    headers: body === undefined ? undefined : { "content-type": "application/json" },
    body,
  });
}

async function waitForPublic(addresses: readonly string[], gateway: Awaited<ReturnType<typeof startGatewayWithOutput>>): Promise<void> {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (gateway.process.exitCode !== null) throw new Error(`Gateway exited before public Caddy readiness (${gateway.process.exitCode}): ${gateway.stderr}`);
    try {
      const statuses = await Promise.all(addresses.map(async (address) => (await request(address, "/healthz")).status));
      if (statuses.every((status) => status > 0)) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error(`Embedded Caddy did not become ready on the plugin listeners: ${gateway.stderr}`);
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
    let gateway: Awaited<ReturnType<typeof startGatewayWithOutput>> | undefined;

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

      gateway = await startGatewayWithOutput(["--config", config, "serve"]);
      await waitForPublic([captchaAddress, formsAddress, identityAddress], gateway);

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
