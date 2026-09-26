import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { promisify } from "node:util";
import { describe, expect, it } from "vitest";
import { freeAddress } from "../support/http.js";

const execFileAsync = promisify(execFile);
const coreRoot = join(import.meta.dirname, "../..");
const externalBuildID = "liapoldus-external-caddy-fixture";

interface FixtureMessage {
  readonly event?: string;
  readonly action?: string;
  readonly ready?: boolean;
  readonly activated?: boolean;
  readonly endpoint?: string;
}

function fixtureProtocol(child: ChildProcess): { next(): Promise<FixtureMessage>; send(value: unknown): void } {
  const lines = createInterface({ input: child.stdout! });
  const buffered: string[] = [];
  const waiters: Array<(line: string) => void> = [];
  lines.on("line", (line) => {
    const waiter = waiters.shift();
    if (waiter) waiter(line);
    else buffered.push(line);
  });
  return {
    next: async () => {
      const line = buffered.shift() ?? await new Promise<string>((resolveLine, reject) => {
        const timer = setTimeout(() => reject(new Error("Timed out waiting for external Caddy fixture.")), 30_000);
        waiters.push((value) => {
          clearTimeout(timer);
          resolveLine(value);
        });
      });
      return JSON.parse(line) as FixtureMessage;
    },
    send: (value) => child.stdin?.write(`${JSON.stringify(value)}\n`),
  };
}

async function buildFixture(name: string, output: string): Promise<void> {
  await execFileAsync("go", ["build", "-o", output, `./tests/fixtures/${name}`], { cwd: coreRoot });
}

async function stopChild(child: ChildProcess | undefined): Promise<void> {
  if (child === undefined || child.exitCode !== null || child.signalCode !== null) return;
  await new Promise<void>((resolveClose) => {
    child.once("close", () => resolveClose());
    child.kill("SIGTERM");
  });
}

describe("supervised custom external Caddy plugin dispatch", () => {
  it("dispatches directly to a plugin and retains the active snapshot when Caddy rejects a candidate", async () => {
    const directory = await mkdtemp(join(tmpdir(), "liapoldus-external-caddy-plugin-"));
    const publicAddress = await freeAddress();
    const pluginAddress = await freeAddress();
    const stateDirectory = join(directory, "state");
    const pluginBinary = join(directory, "plugin-grpc");
    const launcherBinary = join(directory, "plugin-launcher");
    const caddyBinary = join(directory, "liapoldus-caddy");
    const hostBinary = join(directory, "external-caddy-host");
    const initialCaddyfile = join(directory, "initial.Caddyfile");
    const rejectedCandidate = join(directory, "rejected.Caddyfile");
    const initial = `http://${publicAddress} {\n  liapoldus_plugin fixture forms.submit call\n}\n`;
    const candidate = `http://${publicAddress} {\n  liapoldus_plugin fixture forms.not-declared call\n}\n`;
    let plugin: ChildProcess | undefined;
    let host: ChildProcess | undefined;
    let pluginStderr = "";
    let hostStderr = "";

    try {
      await Promise.all([
        buildFixture("plugin-grpc", pluginBinary),
        buildFixture("plugin-process-launcher", launcherBinary),
        buildFixture("external-caddy-custom", caddyBinary),
        buildFixture("external-caddy-plugin-host", hostBinary),
      ]);
      await writeFile(initialCaddyfile, initial, "utf8");
      await writeFile(rejectedCandidate, candidate, "utf8");

      plugin = spawn(launcherBinary, [pluginAddress, pluginBinary], {
        cwd: coreRoot,
        stdio: ["ignore", "ignore", "pipe"],
      });
      plugin.stderr?.on("data", (chunk: Buffer) => { pluginStderr += chunk.toString(); });

      host = spawn(hostBinary, [caddyBinary, stateDirectory, initialCaddyfile, externalBuildID, pluginAddress], {
        cwd: coreRoot,
        stdio: ["pipe", "pipe", "pipe"],
      });
      host.stderr?.on("data", (chunk: Buffer) => { hostStderr += chunk.toString(); });
      const protocol = fixtureProtocol(host);
      expect(await protocol.next()).toMatchObject({ event: "ready", ready: true });

      const firstResponse = await fetch(`http://${publicAddress}/submission`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ sample: "external" }),
      });
      expect(firstResponse.status).toBe(200);
      expect(await firstResponse.json()).toMatchObject({
        method: "POST",
        path: "/submission",
        body: "{\"sample\":\"external\"}",
      });

      protocol.send({ action: "activate", path: rejectedCandidate });
      expect(await protocol.next()).toMatchObject({ action: "activate", activated: false });

      const retainedResponse = await fetch(`http://${publicAddress}/retained`);
      expect(retainedResponse.status).toBe(200);
      expect(await retainedResponse.json()).toMatchObject({
        method: "GET",
        path: "/retained",
      });
      expect(hostStderr).not.toContain(pluginAddress);
      expect(pluginStderr).not.toContain("secret");
    } catch (error) {
      throw new Error(`External Caddy stderr: ${hostStderr}\nPlugin stderr: ${pluginStderr}\n${String(error)}`);
    } finally {
      await stopChild(host);
      await stopChild(plugin);
      await rm(directory, { recursive: true, force: true });
    }
  }, 180_000);
});
