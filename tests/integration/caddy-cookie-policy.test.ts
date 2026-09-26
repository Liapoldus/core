import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { once } from "node:events";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");

async function buildFixture(name: string, output: string): Promise<void> {
  await execFileAsync("go", ["build", "-o", output, `./tests/fixtures/${name}`], { cwd: coreRoot });
}

async function stopChild(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit");
  child.kill("SIGTERM");
  await exited;
}

describe("embedded Caddy cookie policy generation", () => {
  it("atomically replaces dispatch policy and filters the next request", async () => {
    const publicAddress = await freeAddress();
    const pluginAddress = await freeAddress();
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-cookie-policy-"));
    const pluginBinary = join(directory, "plugin-grpc");
    const launcherBinary = join(directory, "plugin-launcher");
    const gatewayBinary = join(directory, "caddy-cookie-policy");
    await Promise.all([
      buildFixture("plugin-grpc", pluginBinary),
      buildFixture("plugin-process-launcher", launcherBinary),
      buildFixture("caddy-cookie-policy", gatewayBinary),
    ]);

    const plugin = spawn(launcherBinary, [pluginAddress, pluginBinary], { cwd: coreRoot, stdio: ["ignore", "ignore", "pipe"] });
    const fixture = spawn(gatewayBinary, [publicAddress, pluginAddress], { cwd: coreRoot, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    fixture.stdout?.on("data", (chunk: Buffer) => { stdout += chunk.toString(); });
    fixture.stderr?.on("data", (chunk: Buffer) => { stderr += chunk.toString(); });
    try {
      const finished = once(fixture, "exit");
      const [code] = await finished;
      expect(code).toBe(0, `${stderr}\n${stdout}`);
      const report = JSON.parse(stdout) as { before: Array<{ name: string; value: string }>; after: Array<{ name: string; value: string }> };
      expect(report.before).toEqual([]);
      expect(report.after).toEqual([{ name: "session", value: "allowed" }]);
    } finally {
      await stopChild(fixture);
      await stopChild(plugin);
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);

  it("accepts cookie policy for a stream-only capability", async () => {
    const publicAddress = await freeAddress();
    const pluginAddress = await freeAddress();
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-caddy-cookie-policy-stream-"));
    const pluginBinary = join(directory, "http-stream-plugin");
    const launcherBinary = join(directory, "plugin-launcher");
    const gatewayBinary = join(directory, "caddy-cookie-policy-stream");
    await Promise.all([
      buildFixture("http-stream-plugin", pluginBinary),
      buildFixture("plugin-process-launcher", launcherBinary),
      buildFixture("caddy-cookie-policy-stream", gatewayBinary),
    ]);

    const plugin = spawn(launcherBinary, [pluginAddress, pluginBinary], { cwd: coreRoot, stdio: ["ignore", "ignore", "pipe"] });
    const fixture = spawn(gatewayBinary, [publicAddress, pluginAddress], { cwd: coreRoot, stdio: ["ignore", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    fixture.stdout?.on("data", (chunk: Buffer) => { stdout += chunk.toString(); });
    fixture.stderr?.on("data", (chunk: Buffer) => { stderr += chunk.toString(); });
    try {
      const [code] = await once(fixture, "exit");
      if (code !== 0) throw new Error(`stream-only Caddy fixture exited ${String(code)}: ${stderr}\n${stdout}`);
      expect(JSON.parse(stdout)).toEqual({ ready: true });
    } finally {
      await stopChild(fixture);
      await stopChild(plugin);
      await rm(directory, { recursive: true, force: true });
    }
  }, 120_000);
});
