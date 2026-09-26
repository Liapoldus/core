import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { freeAddress } from "../support/http.js";

const coreRoot = fileURLToPath(new URL("../..", import.meta.url));
const execFileAsync = promisify(execFile);

async function buildFixture(name: string, output: string): Promise<void> {
  await execFileAsync("go", ["build", "-o", output, `./tests/fixtures/${name}`], { cwd: coreRoot });
}

async function waitForHTTP(address: string, child: ChildProcess): Promise<void> {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    if (child.exitCode !== null) throw new Error("Caddy fixture exited before becoming ready");
    try {
      const response = await fetch(`http://${address}/`);
      response.body?.cancel();
      return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  }
  throw new Error("Caddy fixture did not become ready");
}

async function stopChild(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit");
  child.kill("SIGTERM");
  await exited;
}

describe("Caddy Liapoldus call handler", () => {
  it("dispatches a native Caddyfile call directly to the configured plugin instance", async () => {
    const publicAddress = await freeAddress();
    const pluginAddress = await freeAddress();
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-plugin-call-"));
    const pluginBinary = join(directory, "plugin-grpc");
    const launcherBinary = join(directory, "plugin-launcher");
    const caddyBinary = join(directory, "caddy-plugin");
    const configPath = join(directory, "runtime.Caddyfile");
    await Promise.all([
      buildFixture("plugin-grpc", pluginBinary),
      buildFixture("plugin-process-launcher", launcherBinary),
      buildFixture("caddy-plugin", caddyBinary),
    ]);
    await writeFile(configPath, `http://${publicAddress} {\n  liapoldus_plugin fixture forms.submit call\n}\n`, "utf8");

    const plugin = spawn(launcherBinary, [pluginAddress, pluginBinary], {
      cwd: coreRoot,
      stdio: ["ignore", "ignore", "pipe"],
    });
    let pluginStderr = "";
    plugin.stderr?.on("data", (chunk: Buffer) => { pluginStderr += chunk.toString(); });
    const caddy = spawn(caddyBinary, [configPath, pluginAddress], {
      cwd: coreRoot,
      stdio: ["ignore", "ignore", "pipe"],
    });
    let caddyStderr = "";
    caddy.stderr?.on("data", (chunk: Buffer) => { caddyStderr += chunk.toString(); });
    try {
      await waitForHTTP(publicAddress, caddy);
      const response = await fetch(`http://${publicAddress}/submission`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          authorization: "Bearer should-not-cross-boundary",
          cookie: "session=synthetic-first; theme=synthetic-unlisted; session=synthetic-second",
        },
        body: JSON.stringify({ sample: "payload" }),
      });
      expect(response.status).toBe(200);
      expect(await response.json()).toMatchObject({
        method: "POST",
        path: "/submission",
        body: "{\"sample\":\"payload\"}",
        authorizationPresent: false,
        cookieHeaderPresent: false,
        cookies: [
          { name: "session", value: "synthetic-first" },
          { name: "session", value: "synthetic-second" },
        ],
      });
      const cookieResponse = await fetch(`http://${publicAddress}/set-cookie`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ sample: "payload" }),
      });
      expect(cookieResponse.status).toBe(200);
      expect(cookieResponse.headers.get("set-cookie")).toContain("session=synthetic-cookie-value");
      expect(cookieResponse.headers.get("set-cookie")).toContain("HttpOnly");
      expect(cookieResponse.headers.get("set-cookie")).toContain("Secure");

      const invalidCookieResponse = await fetch(`http://${publicAddress}/invalid-cookies`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ sample: "payload" }),
      });
      expect(invalidCookieResponse.status).toBe(502);
      expect(invalidCookieResponse.headers.has("set-cookie")).toBe(false);
    } catch (error) {
      throw new Error(`Caddy stderr: ${caddyStderr}\nPlugin stderr: ${pluginStderr}\n${String(error)}`);
    } finally {
      await stopChild(caddy);
      await stopChild(plugin);
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);

  it.each([
    ["forms.submit", "http-stream"],
    ["forms.missing", "call"],
  ])("rejects undeclared capability/mode %s/%s before activating Caddy", async (capability, mode) => {
    const publicAddress = await freeAddress();
    const pluginAddress = await freeAddress();
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-plugin-mode-"));
    const pluginBinary = join(directory, "plugin-grpc");
    const launcherBinary = join(directory, "plugin-launcher");
    const caddyBinary = join(directory, "caddy-plugin");
    const configPath = join(directory, "runtime.Caddyfile");
    await Promise.all([
      buildFixture("plugin-grpc", pluginBinary),
      buildFixture("plugin-process-launcher", launcherBinary),
      buildFixture("caddy-plugin", caddyBinary),
    ]);
    await writeFile(configPath, `http://${publicAddress} {\n  liapoldus_plugin fixture ${capability} ${mode}\n}\n`, "utf8");
    const plugin = spawn(launcherBinary, [pluginAddress, pluginBinary], {
      cwd: coreRoot,
      stdio: ["ignore", "ignore", "ignore"],
    });
    const caddy = spawn(caddyBinary, [configPath, pluginAddress], {
      cwd: coreRoot,
      stdio: ["ignore", "ignore", "pipe"],
    });
    let caddyStderr = "";
    caddy.stderr?.on("data", (chunk: Buffer) => { caddyStderr += chunk.toString(); });
    try {
      await once(caddy, "exit");
      expect(caddy.exitCode).not.toBe(0);
      expect(caddyStderr).not.toContain(pluginAddress);
    } finally {
      await stopChild(caddy);
      await stopChild(plugin);
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
